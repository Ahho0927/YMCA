package lms

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// 브라우저를 띄우지 않고도 net/http.Client가 쿠키를 기억하게 해서, 사람이 브라우저에서 하는 로그인 절차를 HTTP 요청으로 재현한다.
// 전체 흐름은 LMS 접속 -> GakuNin/OpenAM 인증 -> 필요 시 2FA -> SAMLResponse 제출 -> 최종 LMS 대시보드 HTML 저장 순서다.
const (
	lmsLoginURL      = "https://lms.ynu.ac.jp/lms/lginLgir/"
	openAMBaseURL    = "https://gakunin-idp.ynu.ac.jp/openam"
	openAMAuthURL    = "https://gakunin-idp.ynu.ac.jp/openam/json/authenticate"
	openAMUsersURL   = "https://gakunin-idp.ynu.ac.jp/openam/json/users"
	lmsHomeMarkerURL = "https://lms.ynu.ac.jp/lms/homeHoml/"
)

// lmsInitialState는 LMS 첫 화면에서 뽑아낸 "GakuNin으로 넘어가기 직전" 상태다.
// initialSID는 아직 최종 로그인 SID가 아니라, LMS가 로그인 흐름을 추적하기 위해 form action URL에 붙여 둔 임시 SID다.
// forwardURL은 실제로 POST해야 하는 gakuninForward 주소이고, payload는 그 form 안에 있던 hidden input/select/button 값들이다.
type lmsInitialState struct {
	initialSID string
	forwardURL string
	payload    url.Values
}

// xuiState는 LMS에서 OpenAM XUI 로그인 화면으로 넘어간 뒤 URL fragment에서 읽어낸 인증 파라미터 묶음이다.
// OpenAM의 /json/authenticate endpoint를 호출할 때 realm, goto, spEntityID 같은 값이 그대로 필요하다.
type xuiState struct {
	xuiURL     string
	authParams url.Values
	realm      string
	gotoURL    string
}

// loginResult는 OpenAM 인증이 끝난 뒤 얻는 결과다.
// 학내망에서는 2FA 없이 tokenID/successURL이 바로 올 수 있고, 학외망에서는 HOTP2 단계를 거친 뒤 같은 결과가 올 수 있다.
type loginResult struct {
	tokenID    string
	successURL string
	used2FA    bool
	raw        map[string]any
}

// samlForm은 HTML 안의 자동 제출 form을 Go 코드가 대신 제출하기 위해 쓰는 작은 구조체다.
// SAMLResponse form과 GakuNin-LMS bridge form 둘 다 "action URL + input payload" 형태라 같은 타입으로 다룬다.
type samlForm struct {
	action  string
	payload url.Values
}

// formData/selectData는 HTML form을 아주 단순하게 파싱해서 필요한 부분만 보관하는 구조체다.
// 정식 브라우저 DOM이 아니라, 이 로그인 흐름에서 필요한 form/action/input/select/button 정도만 모은다.
type formData struct {
	attrs   map[string]string
	inputs  []map[string]string
	selects []selectData
	buttons []map[string]string
	body    string
}

type selectData struct {
	attrs   map[string]string
	options []map[string]string
}

// getInitialLMSState는 LMS 로그인 첫 페이지를 받아서 GakuNin으로 넘기는 form 정보를 추출한다.
// 사람이 브라우저에서 "GakuNin 로그인" 버튼을 누르면 form이 제출되는데, 이 함수는 그 form의 action/payload를 코드로 복원한다.
func (c *client) getInitialLMSState() (lmsInitialState, error) {
	resp, body, err := c.do("GET", lmsLoginURL, nil, nil, nil)
	if err != nil {
		return lmsInitialState{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return lmsInitialState{}, fmt.Errorf("LMS login page returned %d", resp.StatusCode)
	}

	forms := collectForms(body)
	form := findForm(forms, "id", "gakuninLoginForm")
	if form == nil {
		return lmsInitialState{}, errors.New("form#gakuninLoginForm was not found")
	}

	action := form.attrs["action"]
	if action == "" {
		return lmsInitialState{}, errors.New("gakuninLoginForm has no action")
	}
	action = absoluteURL(resp.Request.URL.String(), action)
	sid := extractSID(action)
	if sid == "" {
		return lmsInitialState{}, errors.New("could not extract LMS session identifier")
	}

	payload := url.Values{}
	for _, input := range form.inputs {
		name := input["name"]
		if name != "" {
			payload.Set(name, input["value"])
		}
	}
	for _, sel := range form.selects {
		name := sel.attrs["name"]
		if name != "" {
			payload.Set(name, selectedOptionValue(sel))
		}
	}
	for _, button := range form.buttons {
		if name := button["name"]; name != "" {
			payload.Set(name, button["value"])
			break
		}
	}
	// LMS 로그인 화면 자체에 lang select/input이 있으면 그 값도 원하는 언어로 덮어쓴다.
	// 단, 이것만으로는 OpenAM 2FA 메일이나 최종 LMS 세션 언어가 바뀌지 않을 수 있어서,
	// do()에서 모든 요청에 Accept-Language를 넣고, fetchDashboard()에서 post-login ChangeLang도 별도로 시도한다.
	if c.lang != "" {
		payload.Set("lang", c.lang)
	}

	return lmsInitialState{initialSID: sid, forwardURL: action, payload: payload}, nil
}

func (c *client) startGakuninForward(state lmsInitialState) (xuiState, error) {
	// LMS 로그인 화면의 form을 제출한다. 이 요청은 "나는 GakuNin 인증으로 로그인하겠다"는 신호다.
	// 성공하면 LMS가 OpenAM XUI 화면으로 redirect시키고, Go client는 redirect를 자동으로 따라간다.
	headers := map[string]string{
		"Referer": lmsLoginURL,
		"Origin":  "https://lms.ynu.ac.jp",
	}
	resp, body, err := c.do("POST", state.forwardURL, headers, state.payload, nil)
	if err != nil {
		return xuiState{}, err
	}
	if resp.StatusCode >= 400 {
		return xuiState{}, fmt.Errorf("gakuninForward returned %d", resp.StatusCode)
	}
	finalURL := resp.Request.URL
	// Go의 URL.Host에는 :443 같은 포트가 붙을 수 있으므로 Hostname()으로 도메인만 비교한다.
	if !sameOrigin(finalURL, mustURL(c.idpOrigin)) || !strings.HasPrefix(finalURL.Path, "/openam/XUI") {
		return xuiState{}, errors.New("authentication did not reach the expected OpenAM login page")
	}
	_ = body

	// OpenAM XUI URL은 # 뒤 fragment에 realm/goto/spEntityID 같은 값들을 넣어 둔다.
	// 브라우저 JavaScript라면 이 값을 읽어서 /json/authenticate를 호출하지만,
	// 우리는 브라우저가 아니므로 직접 fragment를 파싱해서 authParams를 만든다.
	fragment := finalURL.Fragment
	queryLike := ""
	if idx := strings.Index(fragment, "&"); idx >= 0 {
		queryLike = fragment[idx+1:]
	}
	params, _ := url.ParseQuery(queryLike)
	xuiGoto := lastValue(params, "goto")
	authGoto := normalizeAuthenticateGoto(xuiGoto)

	authParams := url.Values{}
	authParams.Set("realm", valueOr(lastValue(params, "realm"), "/usr"))
	authParams.Set("forward", valueOr(lastValue(params, "forward"), "true"))
	authParams.Set("spEntityID", valueOr(lastValue(params, "spEntityID"), "https://lms.ynu.ac.jp/shibboleth-sp"))
	authParams.Set("ForceAuth", valueOr(lastValue(params, "ForceAuth"), "true"))
	authParams.Set("goto", authGoto)
	authParams.Set("AMAuthCookie", lastValue(params, "AMAuthCookie"))
	if token := lastValue(params, "sessionUpgradeSSOTokenId"); token != "" {
		authParams.Set("sessionUpgradeSSOTokenId", token)
	}

	return xuiState{
		xuiURL:     finalURL.String(),
		authParams: authParams,
		realm:      authParams.Get("realm"),
		gotoURL:    authGoto,
	}, nil
}

// openAMAuthFlow는 OpenAM의 실제 사용자 인증을 수행한다.
// 첫 호출로 "어떤 입력이 필요한지" callback 목록을 받고, 그 callback 안에 아이디/비밀번호를 채워 다시 보낸다.
// 그 다음 결과가 tokenId/successUrl이면 학내망처럼 2FA 없이 성공한 것이고, stage=HOTP2면 이메일 2FA가 필요한 상황이다.
func (c *client) openAMAuthFlow(xui xuiState, username, password, code string) (loginResult, error) {
	beginData, err := c.postAuthenticate(xui, map[string]any{}, "begin authenticate")
	if err != nil {
		return loginResult{}, err
	}
	if _, err := requireCallbacks(beginData, "OpenLDAP1"); err != nil {
		return loginResult{}, err
	}

	primaryPayload, err := fillPrimaryCredentials(beginData, username, password)
	if err != nil {
		return loginResult{}, err
	}
	primaryData, err := c.postAuthenticate(xui, primaryPayload, "primary authenticate")
	if err != nil {
		return loginResult{}, err
	}

	tokenID := stringValue(primaryData["tokenId"])
	successURL := stringValue(primaryData["successUrl"])
	if tokenID != "" || successURL != "" {
		if tokenID != "" {
			c.addOpenAMTokenCookie(tokenID)
		}
		return loginResult{tokenID: tokenID, successURL: successURL, used2FA: false, raw: primaryData}, nil
	}

	stage := stringValue(primaryData["stage"])
	if stage != "HOTP2" {
		if stage == "OpenLDAP1" {
			return loginResult{}, errors.New("primary auth stayed at OpenLDAP1; credentials likely failed")
		}
		return loginResult{}, fmt.Errorf("expected tokenId/successUrl or HOTP2 after primary auth, got %q", stage)
	}

	// ==============================================
	if code == "" {
		code, err = c.promptCode()
		if err != nil {
			return loginResult{}, err
		}
	}
	twoFAPayload, err := fill2FACode(primaryData, code, 0)
	if err != nil {
		return loginResult{}, err
	}
	twoFAData, err := c.postAuthenticate(xui, twoFAPayload, "2FA authenticate")
	if err != nil {
		return loginResult{}, err
	}
	if stringValue(twoFAData["stage"]) == "HOTP2" {
		return loginResult{}, errors.New("2FA response stayed at HOTP2; code may be wrong")
	}
	if _, hasCallbacks := twoFAData["callbacks"]; hasCallbacks && stringValue(twoFAData["tokenId"]) == "" {
		return loginResult{}, fmt.Errorf("2FA returned another callback stage: %v", twoFAData["stage"])
	}

	tokenID = stringValue(twoFAData["tokenId"])
	successURL = stringValue(twoFAData["successUrl"])
	if tokenID != "" {
		c.addOpenAMTokenCookie(tokenID)
	}
	return loginResult{tokenID: tokenID, successURL: successURL, used2FA: true, raw: twoFAData}, nil
}

// validateOpenAMSession은 OpenAM이 방금 만든 세션을 실제로 인정하는지 확인한다.
// idFromSession은 "현재 쿠키를 가진 사용자가 누구인가"를 묻는 요청이다.
// 일부 환경에서는 이 확인이 실패해도 successURL로 SAML 흐름을 계속 탈 수 있으므로, tokenID/successURL이 있으면 너무 일찍 멈추지 않는다.
func (c *client) validateOpenAMSession(xui xuiState, result loginResult) error {
	api, _ := url.Parse(openAMUsersURL)
	q := api.Query()
	q.Set("_action", "idFromSession")
	q.Set("realm", xui.realm)
	api.RawQuery = q.Encode()

	resp, body, err := c.do("POST", api.String(), openAMHeaders(xui.xuiURL), nil, map[string]any{})
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		if result.tokenID != "" || result.successURL != "" {
			return nil
		}
		return fmt.Errorf("idFromSession returned %d: %s", resp.StatusCode, trimForError(body))
	}

	var data any
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return err
	}
	return c.validateGoto(xui)
}

// validateGoto는 OpenAM에게 "로그인 후 이동하려는 goto URL이 허용된 목적지인가"를 확인시키는 단계다.
// 보안상 인증 서버는 아무 URL로나 redirect하면 안 되므로, 보통 이런 검증 API를 둔다.
func (c *client) validateGoto(xui xuiState) error {
	candidates := []map[string]any{
		{"goto": xui.gotoURL},
		{"url": xui.gotoURL},
		{"successURL": xui.gotoURL},
	}
	var lastErr string
	for _, payload := range candidates {
		api, _ := url.Parse(openAMUsersURL)
		q := api.Query()
		q.Set("_action", "validateGoto")
		api.RawQuery = q.Encode()
		resp, body, err := c.do("POST", api.String(), openAMHeaders(xui.xuiURL), nil, payload)
		if err != nil {
			return err
		}
		if resp.StatusCode < 400 {
			return nil
		}
		lastErr = fmt.Sprintf("%d: %s", resp.StatusCode, trimForError(body))
	}
	return fmt.Errorf("all validateGoto attempts failed; last error: %s", lastErr)
}

// followSAMLAndLMS는 OpenAM 인증 결과를 LMS 로그인 세션으로 바꾸는 단계다.
// OpenAM은 SAMLResponse라는 "이 사용자는 인증 완료됨" 증명서를 HTML form 안에 넣어 주고,
// 브라우저라면 그 form을 자동 제출한다. 이 함수는 그 자동 제출을 Go 코드로 대신한다.
func (c *client) followSAMLAndLMS(xui xuiState, result loginResult) (string, error) {
	nextPath := xui.gotoURL
	if result.successURL != "" {
		nextPath = result.successURL
	}
	gotoURL, err := gotoToAbsolute(nextPath)
	if err != nil {
		return "", err
	}
	resp, body, err := c.do("GET", gotoURL, nil, nil, nil)
	if err != nil {
		return "", err
	}
	finalResp := resp
	finalBody := body

	if form, ok := extractSAMLForm(body, resp.Request.URL.String()); ok {

		headers := map[string]string{
			"Origin":  "https://gakunin-idp.ynu.ac.jp",
			"Referer": resp.Request.URL.String(),
		}
		samlResp, samlBody, err := c.do("POST", form.action, headers, form.payload, nil)
		if err != nil {
			return "", err
		}
		finalResp = samlResp
		finalBody = samlBody
	} else {
	}

	if !c.isLMSHomeURL(finalResp.Request.URL) {
		if form, ok := extractNamedPostForm(finalBody, finalResp.Request.URL.String(), "gknssoform", "/lms/lginLgir/gakuninLogin"); ok {

			headers := map[string]string{
				"Origin":  "https://lms.ynu.ac.jp",
				"Referer": finalResp.Request.URL.String(),
			}
			bridgeResp, bridgeBody, err := c.do("POST", form.action, headers, form.payload, nil)
			if err != nil {
				return "", err
			}
			finalResp = bridgeResp
			finalBody = bridgeBody
			_ = finalBody
		}
	}

	if !c.isLMSHomeURL(finalResp.Request.URL) && sameOrigin(finalResp.Request.URL, mustURL(c.lmsOrigin)) {
		followResp, _, err := c.do("GET", finalResp.Request.URL.String(), nil, nil, nil)
		if err != nil {
			return "", err
		}
		finalResp = followResp
	}

	finalURL := finalResp.Request.URL.String()
	if !c.isLMSHomeURL(finalResp.Request.URL) {
		return "", errors.New("authentication did not reach the LMS dashboard")
	}
	finalSID := extractSID(finalURL)
	if finalSID == "" {
		return "", errors.New("LMS dashboard did not contain a session identifier")
	}
	return finalSID, nil
}

func (c *client) postAuthenticate(xui xuiState, payload map[string]any, label string) (map[string]any, error) {
	api, _ := url.Parse(openAMAuthURL)
	api.RawQuery = xui.authParams.Encode()
	resp, body, err := c.do("POST", api.String(), openAMHeaders(xui.xuiURL), nil, payload)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s returned %d: %s", label, resp.StatusCode, trimForError(body))
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return nil, fmt.Errorf("%s returned non-JSON response: %w", label, err)
	}
	return data, nil
}

// openAMHeaders는 OpenAM XUI가 AJAX 요청을 보낼 때 쓰는 헤더들을 흉내 낸다.
// 특히 Accept-API-Version과 X-Requested-With는 OpenAM REST API가 기대하는 경우가 있어서 유지한다.
func openAMHeaders(referer string) map[string]string {
	if referer == "" {
		referer = "https://gakunin-idp.ynu.ac.jp/openam/XUI/"
	}
	return map[string]string{
		"Accept":             "application/json, text/javascript, */*; q=0.01",
		"Content-Type":       "application/json",
		"Accept-API-Version": "protocol=1.0,resource=2.0",
		"X-Requested-With":   "XMLHttpRequest",
		"Cache-Control":      "no-cache",
		"Origin":             "https://gakunin-idp.ynu.ac.jp",
		"Referer":            referer,
	}
}

// addOpenAMTokenCookie는 OpenAM authenticate 응답의 tokenId를 OpenAM 세션 쿠키로 넣는다.
// 브라우저의 XUI JavaScript가 원래 해 주는 일을 Go 코드가 대신하는 부분이다.
func (c *client) addOpenAMTokenCookie(tokenID string) {
	u, _ := url.Parse(c.resolve(openAMBaseURL))
	c.jar.SetCookies(u, []*http.Cookie{{
		Name:     "iPlanetDirectoryPro",
		Value:    tokenID,
		Path:     "/openam",
		Secure:   true,
		HttpOnly: true,
	}})
}

// requireCallbacks는 OpenAM 인증 응답이 우리가 기대하는 callback 단계인지 검사한다.
// callback은 "여기에 값을 채워서 다시 보내라"는 서버의 입력 요청 목록이다.
func requireCallbacks(data map[string]any, expectedStage string) ([]any, error) {
	if expectedStage != "" && stringValue(data["stage"]) != expectedStage {
		return nil, fmt.Errorf("expected stage %s, got %v", expectedStage, data["stage"])
	}
	callbacks, ok := data["callbacks"].([]any)
	if !ok {
		return nil, errors.New("authenticate response did not include callbacks")
	}
	if stringValue(data["authId"]) == "" {
		return nil, errors.New("authenticate response did not include authId")
	}
	return callbacks, nil
}

// fillPrimaryCredentials는 OpenLDAP1 단계의 callback 안에 username/password를 채운다.
// OpenAM은 고정된 username/password 필드가 아니라 IDToken1/IDToken2라는 callback input 이름을 사용한다.
func fillPrimaryCredentials(authState map[string]any, username, password string) (map[string]any, error) {
	callbacks, err := requireCallbacks(authState, "OpenLDAP1")
	if err != nil {
		return nil, err
	}
	sawUser := false
	sawPass := false
	for _, item := range callbacks {
		callback, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch stringValue(callback["type"]) {
		case "NameCallback":
			sawUser = setCallbackInput(callback, "IDToken1", username) || sawUser
		case "PasswordCallback":
			sawPass = setCallbackInput(callback, "IDToken2", password) || sawPass
		}
	}
	if !sawUser || !sawPass {
		return nil, errors.New("OpenLDAP1 callbacks did not contain IDToken1/IDToken2")
	}
	return authState, nil
}

// fill2FACode는 HOTP2 단계의 callback 안에 이메일 2FA 코드를 채운다.
// ConfirmationCallback의 IDToken2=0은 화면에서 "login/확인" 버튼을 선택한 것과 같은 의미로 관찰된 값이다.
func fill2FACode(twoFAState map[string]any, code string, option int) (map[string]any, error) {
	callbacks, err := requireCallbacks(twoFAState, "HOTP2")
	if err != nil {
		return nil, err
	}
	sawCode := false
	sawConfirm := false
	for _, item := range callbacks {
		callback, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch stringValue(callback["type"]) {
		case "PasswordCallback":
			sawCode = setCallbackInput(callback, "IDToken1", code) || sawCode
		case "ConfirmationCallback":
			sawConfirm = setCallbackInput(callback, "IDToken2", option) || sawConfirm
		}
	}
	if !sawCode || !sawConfirm {
		return nil, errors.New("HOTP2 callbacks did not contain expected IDToken1/IDToken2")
	}
	return twoFAState, nil
}

// setCallbackInput은 callback의 input 배열에서 name이 일치하는 항목을 찾아 value를 바꾼다.
// Go에서는 JSON을 map[string]any로 풀었기 때문에, 타입 단언을 조심스럽게 하면서 내부 값을 수정한다.
func setCallbackInput(callback map[string]any, inputName string, value any) bool {
	inputs, ok := callback["input"].([]any)
	if !ok {
		return false
	}
	for _, item := range inputs {
		input, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringValue(input["name"]) == inputName {
			input["value"] = value
			return true
		}
	}
	return false
}

// extractSAMLForm은 OpenAM이 내려준 HTML에서 SAMLResponse input을 가진 form을 찾는다.
// 이 form은 보통 브라우저에서 자동 제출되지만, 우리는 HTML 문자열만 받았으므로 직접 찾아서 POST해야 한다.
func extractSAMLForm(htmlText, baseURL string) (samlForm, bool) {
	for _, form := range collectForms(htmlText) {
		payload := inputsToValues(form.inputs)
		if payload.Get("SAMLResponse") == "" {
			continue
		}
		action := form.attrs["action"]
		if action == "" {
			return samlForm{}, false
		}
		return samlForm{action: absoluteURL(baseURL, action), payload: payload}, true
	}
	return samlForm{}, false
}

// extractNamedPostForm은 이름이나 action 일부가 일치하는 일반 POST form을 찾는다.
// SAML 제출 뒤 LMS가 gknssoform 같은 중간 bridge form을 내려줄 때 이 함수로 찾아서 다시 제출한다.
func extractNamedPostForm(htmlText, baseURL, formName, actionContains string) (samlForm, bool) {
	for _, form := range collectForms(htmlText) {
		action := form.attrs["action"]
		if formName != "" && form.attrs["name"] != formName {
			continue
		}
		if actionContains != "" && !strings.Contains(action, actionContains) {
			continue
		}
		if action == "" {
			continue
		}
		return samlForm{action: absoluteURL(baseURL, action), payload: inputsToValues(form.inputs)}, true
	}
	return samlForm{}, false
}

// extractFormAction은 특정 id를 가진 form의 action URL만 가져온다.
// 최종 대시보드에서 ChangeLang form을 찾을 때 사용한다.
func extractFormAction(htmlText, formID string) string {
	form := findForm(collectForms(htmlText), "id", formID)
	if form == nil {
		return ""
	}
	return form.attrs["action"]
}

// findForm은 collectForms 결과에서 특정 속성값을 가진 form을 찾는다.
// id="gakuninLoginForm"이나 id="ChangeLang"처럼 목적이 분명한 form을 고를 때 쓴다.
func findForm(forms []formData, attr, value string) *formData {
	for idx := range forms {
		if forms[idx].attrs[strings.ToLower(attr)] == value {
			return &forms[idx]
		}
	}
	return nil
}

// inputsToValues는 input 목록을 POST 가능한 url.Values로 바꾼다.
// 같은 name이 여러 번 나올 수 있으므로 Set이 아니라 Add를 사용한다.
func inputsToValues(inputs []map[string]string) url.Values {
	values := url.Values{}
	for _, input := range inputs {
		name := input["name"]
		if name != "" {
			values.Add(name, input["value"])
		}
	}
	return values
}

// selectedOptionValue는 select 태그에서 선택된 option 값을 찾는다.
// selected 속성이 없으면 브라우저처럼 첫 번째 option을 기본값으로 본다.
func selectedOptionValue(sel selectData) string {
	if len(sel.options) == 0 {
		return ""
	}
	selected := sel.options[0]
	for _, option := range sel.options {
		if _, ok := option["selected"]; ok {
			selected = option
			break
		}
	}
	return selected["value"]
}

// extractSID는 URL이나 HTML 안에 들어있는 ;SID=... 값을 정규식으로 뽑는다.
// 이 LMS는 쿠키뿐 아니라 URL path parameter 방식의 SID도 같이 쓰므로 이 값이 매우 중요하다.
func extractSID(text string) string {
	re := regexp.MustCompile(`;SID=([^/?#'"\s<>]+)`)
	match := re.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

// absoluteURL은 상대 URL을 기준 URL에 붙여 절대 URL로 바꾼다.
// HTML form action은 /lms/...처럼 상대 경로로 올 수 있어서 POST 전에 반드시 절대 주소로 만든다.
func absoluteURL(base, maybeURL string) string {
	baseParsed, err := url.Parse(base)
	if err != nil {
		return maybeURL
	}
	next, err := url.Parse(maybeURL)
	if err != nil {
		return maybeURL
	}
	return baseParsed.ResolveReference(next).String()
}

// normalizeAuthenticateGoto는 OpenAM XUI fragment의 goto를 authenticate API가 기대하는 모양으로 맞춘다.
// 관찰된 동작상 /SSORedirect/... 앞에 /openam을 붙여야 raw HTTP 요청이 브라우저와 같은 모양이 된다.
func normalizeAuthenticateGoto(gotoURL string) string {
	if strings.HasPrefix(gotoURL, "/SSORedirect/") {
		return "/openam" + gotoURL
	}
	return gotoURL
}

// gotoToAbsolute는 successURL/goto 값이 절대 URL인지 상대 URL인지에 따라 실제 요청 가능한 URL로 바꾼다.
func gotoToAbsolute(gotoURL string) (string, error) {
	if gotoURL == "" {
		return "", errors.New("empty goto")
	}
	if strings.HasPrefix(gotoURL, "http://") || strings.HasPrefix(gotoURL, "https://") {
		return gotoURL, nil
	}
	if strings.HasPrefix(gotoURL, "/openam/") {
		return "https://gakunin-idp.ynu.ac.jp" + gotoURL, nil
	}
	if strings.HasPrefix(gotoURL, "/") {
		return openAMBaseURL + gotoURL, nil
	}
	return absoluteURL(openAMBaseURL+"/", gotoURL), nil
}

// isLMSHomeURL은 최종 redirect가 LMS 홈으로 도착했는지 구조적으로 검사한다.
// URL 문자열에는 :443이 붙을 수 있으므로 문자열 contains보다 Hostname()/Path 검사가 안전하다.
func (c *Client) isLMSHomeURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	return sameOrigin(u, mustURL(c.lmsOrigin)) && strings.HasPrefix(u.Path, "/lms/homeHoml/")
}

// valueOr는 빈 문자열이면 기본값을 쓰게 해 주는 작은 도우미다.
func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// normalizeLanguage는 "ja-JP", "ja_jp", "en-US"처럼 들어온 값을 이 코드가 쓰는 짧은 코드로 정리한다.
// 현재 LMS에서 의미 있게 테스트한 값은 ja/en이므로 그 둘만 적극적으로 처리하고 나머지는 소문자 첫 토큰을 사용한다.
func normalizeLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "_", "-")
	if strings.HasPrefix(value, "ja") {
		return "ja"
	}
	if strings.HasPrefix(value, "en") {
		return "en"
	}
	if idx := strings.Index(value, "-"); idx >= 0 {
		return value[:idx]
	}
	return value
}

// acceptLanguageFor는 HTTP Accept-Language 헤더 값을 만든다.
// --lang ja일 때는 매 요청마다 일본어를 최우선으로 알려서, OpenAM 2FA 메일이나 LMS HTML이 이 설정을 참고할 기회를 준다.
func acceptLanguageFor(lang string) string {
	switch normalizeLanguage(lang) {
	case "ja":
		return "ja-JP,ja;q=0.9,en-US;q=0.8,en;q=0.7,ko;q=0.6"
	case "en":
		return "en-US,en;q=0.9,ja;q=0.8,ko;q=0.7"
	default:
		return "ja-JP,ja;q=0.9,en-US;q=0.8,en;q=0.7,ko;q=0.6"
	}
}

// lastValue는 query parameter에 같은 key가 여러 번 있을 때 마지막 값을 고른다.
// 파이썬 PoC도 parse_qs 결과에서 마지막 값을 사용했다.
func lastValue(values url.Values, key string) string {
	items := values[key]
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1]
}

// stringValue는 JSON을 map[string]any로 풀었을 때 안전하게 문자열만 꺼내는 함수다.
func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func trimForError(string) string { return "response details omitted" }
