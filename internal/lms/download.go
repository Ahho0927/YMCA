package lms

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Skip quoted values, which can themselves contain parameter-like text.
var extendedFilename = regexp.MustCompile(`(?i)"(?:[^"\\]|\\.)*"|(?:^|;)\s*(filename\*(?:[0-9]+\*?)?)\s*=`)

func dispositionFilename(header string) string {
	_, params, _ := mime.ParseMediaType(header)
	name := params["filename"]
	// ParseMediaType already decodes RFC 2231/5987 filename* parameters. LMS
	// also sends URL-encoded UTF-8 in the ordinary filename parameter.
	extended := false
	for _, match := range extendedFilename.FindAllStringSubmatch(header, -1) {
		if match[1] != "" {
			extended = true
			break
		}
	}
	if !extended {
		if decoded, err := url.PathUnescape(name); err == nil && utf8.ValidString(decoded) {
			name = decoded
		}
	}
	return name
}

func stableResourceID(n navigation) string {
	u := *mustURL(n.url)
	u.Path = regexp.MustCompile(`(?i);SID=[^/?#;]+`).ReplaceAllString(u.Path, "")
	u.RawPath = ""
	clean := func(v url.Values) url.Values {
		out := url.Values{}
		for k, values := range v {
			lower := strings.ToLower(k)
			if strings.Contains(lower, "token") || strings.Contains(lower, "csrf") || strings.Contains(lower, "session") || lower == "sid" {
				continue
			}
			out[k] = values
		}
		return out
	}
	u.RawQuery = clean(u.Query()).Encode()
	sum := sha256.Sum256([]byte(n.method + "\n" + u.String() + "\n" + clean(n.values).Encode()))
	return fmt.Sprintf("file-%x", sum[:8])
}
func safeFilename(s string) string {
	s = strings.ToValidUTF8(s, "_")
	s = strings.ReplaceAll(s, "\\", "/")
	parts := strings.Split(s, "/")
	s = parts[len(parts)-1]
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, s)
	s = strings.Trim(s, " .")
	if s == "" {
		s = "download"
	}
	base := strings.ToUpper(strings.Split(s, ".")[0])
	for _, name := range []string{"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"} {
		if base == name {
			s = "_" + s
		}
	}
	// Leave room below common 255-byte filesystem limits for collision suffixes.
	// Japanese names need a byte limit as well as the existing rune limit.
	ext := filepath.Ext(s)
	if len(ext) > 32 {
		ext = ""
	}
	stem := strings.TrimSuffix(s, ext)
	byteLimit, runeLimit := 220-len(ext), 160-utf8.RuneCountInString(ext)
	end, count := 0, 0
	for _, r := range stem {
		size := utf8.RuneLen(r)
		if end+size > byteLimit || count >= runeLimit {
			break
		}
		end += size
		count++
	}
	return strings.TrimRight(stem[:end]+ext, " .")
}
func (c *Client) Download(m Material, dir string) (string, error) {
	n := m.request
	if n.url == "" {
		return "", errors.New("material has no verified download request")
	}
	var reader io.Reader
	if n.values != nil {
		reader = strings.NewReader(n.values.Encode())
	}
	req, err := http.NewRequestWithContext(c.ctx, n.method, n.url, reader)
	if err != nil {
		return "", errors.New("invalid download request")
	}
	if err = c.checkURL(req.URL); err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "YMCA/0.1 (YNU LMS CLI)")
	if n.values != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", requestFailure(req, err)
	}
	defer resp.Body.Close()
	if !sameOrigin(resp.Request.URL, mustURL(c.lmsOrigin)) || strings.Contains(resp.Request.URL.Path, "/lginLgir/") || resp.StatusCode == 401 || resp.StatusCode == 403 {
		return "", ErrAuth
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	// Sniff a prefix as servers sometimes label their login HTML as binary data.
	prefix := make([]byte, 4096)
	count, e := io.ReadFull(resp.Body, prefix)
	if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
		return "", errors.New("download was interrupted")
	}
	prefix = prefix[:count]
	lower := strings.ToLower(string(prefix))
	if strings.Contains(lower, "gakuninloginform") || strings.Contains(lower, "/lms/lginlgir/") {
		return "", ErrAuth
	}
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media == "text/html" || strings.Contains(lower, "<!doctype html") || strings.Contains(lower, "<html") {
		return "", errors.New("download returned HTML rather than an attachment; file was not saved")
	}
	name := dispositionFilename(resp.Header.Get("Content-Disposition"))
	if name == "" {
		name = m.Title
	}
	if name == "" {
		name = filepath.Base(mustURL(n.url).Path)
		if decoded, e := url.PathUnescape(name); e == nil {
			name = decoded
		}
	}
	name = safeFilename(name)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".ymca-download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	written, err := io.Copy(tmp, io.MultiReader(strings.NewReader(string(prefix)), resp.Body))
	if err != nil {
		return "", errors.New("download was interrupted")
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		return "", errors.New("download was incomplete")
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	// Reserve a unique final path. Existing downloads are never overwritten.
	for i := 0; i < 10000; i++ {
		candidate := name
		if i > 0 {
			ext := filepath.Ext(name)
			candidate = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), i, ext)
		}
		path := filepath.Join(dir, candidate)
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(e, os.ErrExist) {
			continue
		}
		if e != nil {
			return "", e
		}
		f.Close()
		if err = os.Rename(tmp.Name(), path); err != nil {
			os.Remove(path)
			return "", err
		}
		return path, nil
	}
	return "", errors.New("too many files with the same name")
}
