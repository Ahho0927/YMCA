# YMCA CLI 실사용 검증 체크리스트

> 2026-10-22 다운로드 갱신: 강의명 폴더와 중복 이름 번호를 사용한다. 개별 자료·파일 실패와 첨부 없는 자료는 경고를 남기고 건너뛰며 종료 `0`이다. 기존 체크 표시·피드백은 보존했고 재확인 항목 G05를 추가했다.

> 2026-10-21 문법 갱신: `'*'` 전체 선택과 `download FILE_ID`를 추가했다. 기존 체크 표시·피드백은 유지하며, 새 문법의 확인 항목은 G에 추가했다. 셸 확장을 막도록 모든 `'*'`를 따옴표로 감싼다.


> 2026-10-20 문법 갱신: 자료·레포트·공지 ID를 동작 앞에 배치했다. 기존 체크 표시와 피드백은 보존했다. 해당 표시가 이전 문법으로 확인한 결과라면 B02·B06·C01·C02·D01·D04를 새 예시로 재확인하면 된다.

작성일: 2026-10-16 · 대상: 현재 프로젝트의 `./ymca` · 초기 상태: **실계정 검증 미실행**

먼저 A~D의 기본 검증을 하고, E의 예외 상황과 F의 장시간 검증은 가능한 항목만 진행한다. 체크는 통과한 항목에만 한다. 해당 자료·환경이 없으면 `해당 없음`, 아직 실행하지 않았으면 `미실행`으로 피드백한다.

## 준비

프로젝트 폴더에서 실행한다. 아래는 macOS의 zsh 기준이다.

```sh
./ymca --version
mkdir -p private/validation-results private/validation-downloads
```

테스트 일시: __________ JST  
macOS 버전: __________  
CLI 버전(`dev`여도 그대로 기록): __________  
접속 환경: 학내 / 학외 / VPN __________  
테스트 전 keepalive 활성화 여부: __________

**세션 저장 위치를 하나 선택하고 끝까지 같은 위치를 사용한다.** 기존에 `private/state`로 로그인했다면 첫 번째 함수, 기본 저장 위치로 로그인했다면 두 번째 함수를 실행한다. 둘 다 실행하지 않는다.

```sh
# 선택 1: 기존 개발용 세션
ymca_check() { ./ymca --state-dir private/state "$@"; }
```

```sh
# 선택 2: OS 기본 저장 위치의 세션
ymca_check() { ./ymca "$@"; }
```

이 문서의 `ymca_check`는 입력을 줄이기 위한 셸 함수이며 CLI의 새 명령이 아니다. 새 터미널을 열면 선택한 함수를 다시 정의한다.

종료 코드는 명령 실행 직후 `echo $?`로 확인한다. 정상 및 개별 다운로드 건너뛰기 경고는 `0`, 요청·파싱·초기 다운로드 준비 실패는 `1`, 잘못된 사용법은 `2`, 로그인 필요는 `3`이다.

피드백에는 항목 ID와 증상만 우선 적는다. 비밀번호·인증 코드·쿠키·SID·`state.json`·원본 캡처는 보내지 않는다. JSON 결과와 다운로드 파일은 `private/` 아래에서 확인하고, 필요한 오류 부분만 공유한다.

## A. 기본 실행과 인증

- [x] **A01 — 인자 없는 실행과 도움말**

  ```sh
  ymca_check
  ymca_check courses --help
  ymca_check courses 801105Z materials download --help
  ```

  기대: 로그인이나 네트워크 요청 없이 도움말이 나온다. 최상위 도움말에 `profile`, `topics`, `courses`, `notices`가 있고 `dashboard`는 없다. 다운로드 도움말에 `--all`, `--lesson`, `--file`, `--output`이 있다. 종료 `0`.

- [x] **A02 — 기존 세션 검증**

  ```sh
  ymca_check auth status
  ```

  기대: 유효한 세션은 인증 성공과 종료 `0`. 세션이 없거나 만료됐으면 재로그인 안내와 종료 `3`. 후자라면 아래 명령으로 로그인한 뒤 다시 확인한다.

  ```sh
  ymca_check auth login
  ymca_check auth status
  ```

  이미 확인한 학내·학외 로그인을 모두 다시 할 필요는 없다. 이번에 사용한 환경과 2차 인증 여부를 기록한다. 로그인은 대화형 터미널에서 직접 한다.

## B. 화면의 정보를 올바르게 읽는지 확인

여기서는 브라우저 LMS와 비교한다. 브라우저와 CLI의 학년도·학기 선택 범위가 다른 경우 그 차이를 먼저 기록한다.

- [x] **B01 — 프로필 분리**

  ```sh
  ymca_check profile show
  ymca_check profile show --json
  ```

  기대: 이름·역할·이전 로그인 시각이 LMS와 같다. 공지나 강의 목록이 섞여 나오지 않는다. JSON의 `authenticated_request`는 `true`다.

- [x] **B02 — Topics와 공지**

  ```sh
  ymca_check topics list
  ymca_check notices list
  ```

  기대: Topics는 LMS 홈에 노출된 시스템 안내의 날짜·제목·ID와 같다. 공지는 실제 공지함 목록에 대응하며, 이전 페이지의 공지가 누락되거나 중복되지 않는다. 시스템 안내가 원래 없다면 빈 목록도 정상이다. Topics 본문 조회 명령은 현재 범위가 아니다.

  공지 하나의 ID를 복사한 뒤 실행한다.

  ```sh
  notice_id='여기에 공지 ID 입력'
  ymca_check notices "$notice_id" show
  ```

  기대: 제목·발신자·게시 시각·본문과 줄바꿈이 맞다. 첨부가 있으면 파일 메타데이터가 표시된다. 공지 첨부 다운로드는 아직 지원하지 않는다. 상세 조회는 브라우저 열람과 같이 읽음 상태에 영향을 줄 수 있다.

- [x] **B03 — 강의 목록**

  ```sh
  ymca_check courses list
  ```

  기대: 요일·교시·학기·강의명·교원·가등록 여부가 맞다. 같은 이름의 다른 강의, 온디맨드, 연락 전용 강의도 누락되거나 합쳐지지 않는다. 해당 강의가 없는 계정이면 그 비교만 생략한다.

- [x] **B04 — 강의 상세와 회차**

  강의 하나를 고르고 **실제 값으로 교체**한다. 다운로드 검증에는 크기가 작은 자료가 있는 강의가 편하다.

  ```sh
  course_id='여기에 KOG 내부 ID 입력'
  ymca_check courses "$course_id" show
  ```

  기대: 강의명·7자리 코드·학년도, 회차 이름·설명·실시일·출결·자료/레포트/기타 개수가 맞다. CLI가 출석이나 제출 동작을 실행하지 않는다.

  아래 변수도 실제 값으로 교체한다. 회차 이름의 전각 숫자·괄호·공백까지 출력과 같아야 한다.

  ```sh
  course_code='여기에 7자리 강의코드 입력'
  lesson_name='여기에 정확한 회차 이름 입력'
  ```

- [x] **B05 — 강의 코드와 캐시**

  ```sh
  ymca_check courses "$course_code" show
  ymca_check courses "$course_code" show
  ```

  기대: 내부 ID로 조회한 것과 같은 강의다. 매핑이 없던 첫 조회는 다른 강의의 코드도 확인하므로 느릴 수 있다. 두 번째 조회에서는 전체 강의 코드 검색 진행 메시지가 반복되지 않는다. 이미 매핑이 있었다면 첫 실행부터 검색을 생략할 수 있다.

  같은 코드가 여러 강의에 대응하면 후보 내부 ID를 안내하고 임의로 선택하지 않아야 한다. 실제 중복 코드가 없으면 이 경우는 생략한다. 캐시 시험을 위해 세션 파일을 수정하거나 삭제하지 않는다.

  feedback: 초회검색에서 모든 강의아이디에 해당하는 강의 코드를 검색하는데, 이 과정을 굳이 print하지 않았으면 좋겠음.

- [x] **B06 — 자료 목록·자료 묶음 상세**

  ```sh
  ymca_check courses "$course_id" materials list
  ymca_check courses "$course_id" materials list --lesson "$lesson_name"
  ```

  기대: 첫 명령은 강의 전체의 SRY 자료 묶음, 두 번째는 지정 회차의 묶음만 표시한다. 레포트·시험이 자료 목록에 섞이지 않는다.

  ```sh
  material_id='여기에 해당 회차의 SRY ID 입력'
  ymca_check courses "$course_id" materials "$material_id" show
  file_id='여기에 자료 상세에서 확인한 FIL ID 입력'
  ```

  기대: 자료 설명과 파일 이름·ID·개수가 LMS와 같다. 가능하면 **첨부파일이 두 개 이상인 SRY 묶음**을 선택한다.

## C. 다운로드 범위 검증

출력 경로를 항목별로 나눠 이전 다운로드와 혼동하지 않는다. 다운로드한 파일은 실제로 열어 내용과 크기도 확인한다.

- [x] **C01 — 파일 하나만 다운로드**

  ```sh
  ymca_check courses "$course_id" materials "$material_id" download "$file_id" --output private/validation-downloads/single
  ```

  기대: 선택한 파일 하나만 저장된다. 다른 첨부파일은 저장되지 않는다. PDF 등 원래 파일 형식으로 열리며 로그인 HTML이 들어 있지 않다.

- [x] **C02 — SRY 묶음 전체 다운로드**

  ```sh
  ymca_check courses "$course_id" materials "$material_id" download '*' \
    --output private/validation-downloads/material
  ```

  기대: **그 SRY 묶음의 모든 첨부파일만** 저장된다. 다른 SRY 묶음은 받지 않는다. 파일들은 지정 출력 디렉터리에 직접 저장된다. `materials "$material_id" download`처럼 `--all`을 생략해도 같은 범위를 뜻한다.

- [x] **C03 — 회차 전체 다운로드**

  ```sh
  ymca_check courses "$course_id" materials '*' download \
    --lesson "$lesson_name" --output private/validation-downloads/lesson
  ```

  기대: 선택 회차의 **모든 SRY 묶음에 속한 첨부파일**을 받고 다른 회차는 제외한다. `lesson/강의명/회차이름/` 아래에 저장된다. 가능하면 SRY 묶음이 두 개 이상인 회차로 확인한다.

- [x] **C04 — 강의 전체 다운로드**

  ```sh
  ymca_check courses "$course_id" materials '*' download \
    --output private/validation-downloads/course
  ```

  기대: 해당 강의의 모든 회차에 있는 자료를 받고, 다른 강의는 제외한다. `course/강의명/회차이름/` 아래에 저장된다. 강의 자료가 많다면 이 항목은 작은 강의로 하거나 나중에 진행해도 된다. 첨부 없는 텍스트 자료가 포함돼 경고가 나오면 E03의 기준으로 함께 기록한다.

- [x] **C05 — 같은 파일을 다시 받기**

  C01 명령을 같은 출력 경로로 한 번 더 실행한다.

  기대: 첫 파일이 덮어써지지 않고 `이름 (1).확장자`처럼 별도 파일이 생긴다. 두 파일 모두 정상적으로 열린다.

## D. 레포트·실라버스·출력 형식

- [x] **D01 — 레포트 목록과 상세**

  ```sh
  ymca_check courses "$course_id" reports list
  report_id='여기에 레포트 ID 입력'
  ymca_check courses "$course_id" reports "$report_id" show
  ```

  기대: 실제 제출 기간·마감·제출 상태·과제문이 LMS와 같다. 자료 공개 기한을 제출 마감으로 오인하지 않는다. 초안 답안이나 제출 폼이 본문에 섞이지 않는다. 레포트가 없는 강의는 빈 목록이 정상이다.

  마감 미지정 또는 `24:00` 과제가 있다면 추가 비교한다. JSON의 미확인 마감은 `null`, `24:00`은 다음 날 `00:00+09:00`이어야 한다. 과제 제출은 시험하지 않는다.

- [x] **D02 — 실라버스**

  ```sh
  ymca_check courses "$course_id" syllabus --url-only
  ymca_check courses "$course_code" syllabus
  ```

  기대: URL의 강의코드와 학년도가 맞고, 실제 공개 실라버스와 제목·내용이 일치한다. URL은 중간에 줄바꿈되지 않는다. 코드가 없는 연락 전용 강의는 설명이 있는 오류가 정상이다.

- [x] **D03 — JSON과 진행 메시지 분리**

  ```sh
  ymca_check courses "$course_code" materials list --json \
    > private/validation-results/materials.json \
    2> private/validation-results/materials.stderr.txt
  echo $?
  python3 -m json.tool private/validation-results/materials.json > /dev/null
  echo $?
  ```

  기대: 두 종료 코드 모두 `0`. JSON 파일에 진행 메시지나 오류 문장이 섞이지 않는다. 코드 검색 진행 메시지가 있다면 stderr 파일에만 들어간다. JSON의 원문 제목·회차 이름이 유지된다.

- [x] **D04 — zsh completion**

  ```sh
  source <(./ymca completion zsh)
  ```

  이제 `./ymca courses 실제강의코드 materials ` 뒤에서 Tab을 눌러 `list/download`가 제안되는지 확인한다. `materials 실제SRY_ID ` 뒤에서는 `show/download`, 그 뒤의 `download --`에서는 `--all/--file/--output`이 제안되어야 한다. ID 없는 `materials download --`에서는 `--all/--lesson/--output`이 제안되어야 한다. `./ymca notices 실제공지ID ` 뒤의 `show`도 확인한다. `profile` 뒤의 `show`, `topics` 뒤의 `list`도 확인한다.

  기대: 로그인이나 LMS 요청 없이 동작한다. 강의 코드·회차 이름·파일 ID의 자동 검색은 현재 제공하지 않는다. completion은 `ymca_check` 함수가 아닌 **`./ymca` 명령**으로 시험한다.

## E. 예외 상황 — 가능한 것만

- [x] **E01 — 잘못된 옵션 조합**

  아래 명령을 하나씩 실행하고 바로 `echo $?`를 확인한다.

  ```sh
  ymca_check courses "$course_id" materials "$material_id" download --all --file "$file_id"
  ymca_check courses "$course_id" materials "$material_id" download --all --lesson "$lesson_name"
  ymca_check courses "$course_id" materials download --lesson "$lesson_name"
  ymca_check dashboard
  ```

  기대: 다운로드하지 않고 설명과 종료 `2`. `dashboard`는 대체 명령을 안내한다.

- [x] **E02 — 없는 회차**

  ```sh
  ymca_check courses "$course_id" materials '*' download \
    --lesson 'YMCA-validation-no-such-lesson' \
    --output private/validation-downloads/missing
  ```

  기대: 회차를 찾을 수 없다는 오류와 종료 `1`. 강의 전체나 다른 회차의 파일을 대신 받으면 안 된다. 실제 중복 이름의 회차가 있다면 그 이름으로도 시험해, 모호하다는 오류가 나는지 확인한다.

- [x] **E03 — 빈 강의·빈 회차·첨부 없는 자료**

  실제 사례가 있을 때만 확인한다. 강의 자체가 비어 있으면 `show`의 회차 목록이 비어 있어야 한다. 존재하지만 자료가 없는 회차의 `materials list --lesson …`와 `download --all --lesson …`는 빈 결과로 종료 `0`이어야 한다.

  첨부파일 없는 텍스트 자료는 `materials MATERIAL_ID show`로 읽을 수 있어야 한다. 2026-10-22 변경 후 그 SRY 묶음을 다운로드하면 첨부가 없다는 **경고와 종료 `0`**이 현재 동작이다. 이 자료가 포함된 일괄 다운로드는 나머지 파일을 받고 건너뛴 자료 ID와 이유를 경고로 표시한다.

- [x] **E04 — 일시적 네트워크 실패**

  네트워크를 잠시 끊고 `ymca_check profile show`를 실행한 뒤, 연결을 복구해 같은 명령을 다시 실행한다.

  기대: 끊긴 동안에는 네트워크 오류와 종료 `1`, 복구 후 세션이 유효하면 성공한다. 단순 네트워크 실패를 세션 만료로 기록하거나 바로 재로그인을 강제하지 않는다. 이미 오래된 세션의 자연 만료는 별도로 기록한다.

## F. 세션 유지 — 선택, 장시간 검증

**짧은 기능 검증과 별도로 진행한다.** 백그라운드 서비스는 OS 사용자당 하나이므로 이 문서에서 선택한 세션 저장 위치를 일관되게 사용한다. `enable`은 현재 `./ymca` 경로를 등록하므로 시험 중 실행파일이나 프로젝트 폴더를 옮기지 않는다.

- [x] **F01 — 서비스 설치와 터미널 종료**

  ```sh
  ymca_check auth status
  ymca_check keepalive enable
  ymca_check keepalive status --json
  ```

  기대: `enabled`가 `true`, 잠시 뒤 `running`도 `true`. 터미널을 닫고 새 터미널에서 함수를 다시 정의한 뒤 `keepalive status`를 실행해도 실행 상태가 유지된다. 시작 직후 잠깐 `running: false`라면 약 30초 뒤 다시 확인한다.

- [ ] **F02 — 두 시간 이상 유지되는지**

  새로 로그인한 유효 세션에서 F01을 실행하고 시작 시각을 기록한다. **컴퓨터를 깨어 있고 네트워크에 연결된 상태로 2시간 15분 이상** 둔다. 이 동안 CLI의 조회 명령, `auth status`, LMS 브라우저 새로고침을 하지 않는다.

  중간에 상태를 보려면 **`ymca_check keepalive status`만** 사용한다. 이 명령 자체는 LMS에 요청하지 않는다. 마지막 성공 시각이 실제로 갱신되는지 기록한다.

  마지막에 실행한다.

  ```sh
  ymca_check keepalive status --json
  ymca_check auth status
  ```

  기대: 마지막 성공 시각이 주기적으로 바뀌고 세션이 유효하다. 이것만으로 무기한 유지가 증명되는 것은 아니다. 서버가 절대 만료를 적용한다면 관찰한 시각과 상태를 보고한다.

  시작 시각: __________ / 종료 시각: __________ / 마지막 성공 시각들: __________

- [x] **F03 — 유지하지 않는 대조 실험**

  F02와 **별도 시간대**에 진행한다. `keepalive disable` 후 새로 로그인하고, 잠자기 없이 2시간 15분 이상 LMS 요청을 하지 않은 뒤 `auth status`를 한 번 실행한다. 브라우저 자동 새로고침도 멈춘다.

  기대 관찰: 사용자가 관찰했던 약 2시간 유휴 만료가 재현되는지 확인한다. 종료 `3`이면 만료가 확인된다. 종료 `0`이라면 그 대기 시간에는 만료되지 않은 것이므로 시간을 기록한다. F02 성공과 F03의 만료를 함께 확인해야 요청의 유지 효과를 판단할 근거가 된다. 서로 다른 저장 폴더로 동시에 로그인해도 서버 세션이 독립적이라고 가정하지 않는다.

- [x] **F04 — 잠자기 후 복귀**

  로그인과 `keepalive enable`을 확인한 후 노트북을 잠재웠다가 복귀한다. 먼저 3~5분 잠자기로 시험하고, 가능하면 두 시간 넘는 잠자기도 별도로 시험한다.

  기대: CLI 때문에 잠자기가 방해되거나 컴퓨터가 주기적으로 깨어나지 않는다. 네트워크가 준비됐다면 복귀 후 약 30초 내 세션 검사 시도가 있어야 한다. 만료됐다면 `needs_login`과 재로그인 안내가 나오고, 비밀번호나 이메일 코드를 자동 요청·전송하지 않는다. 네트워크 준비가 늦으면 검사 성공도 늦을 수 있다.

  만료 상태에서 `auth login` 후 `keepalive status`의 `needs_login`이 `false`로 돌아오고 다음 시도 시각이 잡히는지도 확인한다. 재로그인 자체가 활동이므로 다음 정기 요청까지 최대 약 60분이 걸릴 수 있다.

- [x] **F05 — 서비스 제거·로그아웃**

  ```sh
  ymca_check keepalive disable
  ymca_check keepalive status
  ```

  기대: 비활성화되고 작업이 멈춘다. 로그아웃까지 시험하려면 아래를 마지막에 실행한다. 이 경우 계속 사용하려면 다시 로그인해야 한다.

  ```sh
  ymca_check auth logout
  ymca_check profile show
  ```

  기대: 로그아웃 후 조회는 재로그인 안내와 종료 `3`. 로그아웃은 세션 유지도 중단한다. 테스트를 마친 뒤 keepalive는 원래 사용하고 싶었던 상태로 맞춘다.

## G. 와일드카드·위치 파일 선택 — 2026-10-21 추가

기존 A~F의 체크는 이전 검증 기록으로 유지한다. 새 문법은 아래 항목으로 따로 확인한다. 기존 `--all`·`--file`은 계속 동작한다.

- [ ] **G01 — 파일 위치 인자와 전체 파일**

  ```sh
  ymca_check courses "$course_id" materials "$material_id" download "$file_id" --output private/validation-downloads/positional-file
  ymca_check courses "$course_id" materials "$material_id" download '*' --output private/validation-downloads/wildcard-files
  ```

  기대: 첫 명령은 정확히 파일 하나, 두 번째는 선택 SRY 묶음의 모든 파일이다. 기존 C01·C02와 파일 수·내용이 같다.

- [ ] **G02 — 전체 자료와 회차 범위**

  ```sh
  ymca_check courses "$course_id" materials '*' download --output private/validation-downloads/wildcard-course
  ymca_check courses "$course_id" materials '*' download --lesson "$lesson_name" --output private/validation-downloads/wildcard-lesson
  ```

  기대: 강의 전체와 특정 회차 전체가 각각 C04·C03과 같다. 강의/회차 하위 폴더를 유지한다. 별표는 부분 이름 패턴이 아니라 해당 ID 자리의 전체 선택이다.

- [ ] **G03 — 여러 강의·레포트·공지 선택**

  ```sh
  ymca_check courses '*' materials list --json
  ymca_check courses "$course_id" reports '*' show --json
  ymca_check courses '*' reports list --json
  ```

  기대: 현재 대시보드에 보이는 강의만 선택한다. 와일드카드 조회의 JSON은 평평한 배열이고, 레포트 `'*' show`는 과제문도 포함한다. 목록·과제문·마감이 원문과 맞고 시험을 시작하거나 답안을 제출하지 않는다.

  다음 명령은 선택 사항이다. 모든 공지 상세 조회나 전체 강의 다운로드로 요청 수와 다운로드량이 늘 수 있다.

  ```sh
  ymca_check notices '*' show --json
  ymca_check courses '*' materials '*' download '*' --output private/validation-downloads/all-courses --json
  ```

  기대: 각 대상이 중복 처리되지 않고 여러 강의 파일이 강의명 폴더로 나뉜다. 전체 강의 다운로드의 JSON 결과에는 `course_id`가 있다. 일부 조회 실패는 성공 결과를 보존하면서 오류와 비정상 종료를 보고한다. 다운로드의 개별 실패는 `warning`과 stderr 경고로 남기고 종료 `0`이다. 인증 만료 시에는 후속 요청을 중단하고 종료 `3`이다.

- [ ] **G04 — 잘못된 패턴·선택 조합**

  ```sh
  ymca_check courses "$course_id" materials 'SRY*' show
  ymca_check courses "$course_id" materials "$material_id" download 'FIL*'
  ymca_check courses "$course_id" materials "$material_id" download "$file_id" --file "$file_id"
  ```

  기대: 종료 `2`, 다운로드 없음. 위치 파일 인자와 `--file`을 함께 사용하거나 부분 glob 패턴을 쓰면 안 된다. `download '*'`의 `'*'`를 따옴표 없이 시험하지 않는다. CLI가 받기 전에 셸이 로컬 파일명으로 바꿀 수 있다.

- [ ] **G05 — 강의명 폴더와 다운로드 경고 (2026-10-22)**

  ```sh
  ymca_check courses '*' materials '*' download '*' \
    --output private/validation-downloads/course-names --json
  ```

  기대: `course-names/강의명/회차명/`에 저장된다. 같은 이름의 서로 다른 강의는 `강의명`, `강의명 (1)`처럼 나뉜다. 같은 출력 위치로 강의 하나만 ID·코드로 다시 다운로드해도 같은 강의명 폴더를 사용하고 기존 파일을 덮어쓰지 않는다.

  첨부 없는 자료나 접근할 수 없는 파일이 실제로 있으면, stderr에 ID·사유를 포함한 `Warning:`이 표시되고 다른 다운로드가 계속되어야 한다. JSON의 건너뛴 항목은 `warning`, 성공 항목은 `path`를 갖고 종료는 `0`이다. 세션 만료는 경고로 숨기지 않고 종료 `3`으로 중단한다. 이 명령은 G03과 같은 전체 다운로드이므로 함께 확인해도 된다.

## 피드백 양식

모든 항목을 완료할 필요는 없다. **기본 검증 결과를 먼저 보내고 장시간 검증은 나중에 추가**해도 된다. 다음 형태로 보내면 재현과 수정이 쉽다.

```text
검증 일시 / CLI 버전 / macOS 버전:
접속 환경 / 세션 위치(default 또는 private/state):

통과: A01, A02, B01, ...
미실행·해당 없음: F02(나중에), E03(해당 자료 없음), ...

실패 항목 ID:
실행 명령: (비밀번호·코드·세션 값 제외)
기대했던 결과:
실제로 나온 결과 또는 오류:
종료 코드:
항상 재현되는지:
브라우저와의 차이: (개인정보·과제 본문은 필요한 부분만 가려서)

사용하면서 불편했던 점: 
```
