# Changelog

## [0.3.0] - 2026-03-16

### Fixed
- 텔레그램에 "(empty result)" 메시지가 전송되던 문제 수정
- outbox.json 배열 형식 파싱 실패 문제 수정 (Cowork 호환)
- 봇 다중 인스턴스 실행 시 충돌 문제 해결

### Removed
- PollInboxDone 제거 - inbox/outbox 동시 폴링으로 인한 중복 전송 방지

### Changed
- outbox 빈 result 메시지는 전송하지 않고 skip

## [0.2.0] - 2026-03-16

### Added
- 파일 4개로 분리 (main.go, model.go, store.go, bot.go)
- Lock file 메커니즘 (inbox.lock, PID/timestamp 기록, stale 감지)
- 메시지 상태 5단계 (pending → processing → done → sent → error)
- 자동 재시도 로직 (최대 3회, 초과 시 텔레그램 알림)
- sync.Mutex + lock file 이중 동시성 보호
- 구조화된 로깅 (slog, stdout + bot.log 동시 출력)
- Graceful shutdown (SIGINT, SIGTERM)
- 텔레그램 커맨드: /status, /clear, /retry
- 환경변수 추가: LOCK_TIMEOUT_MINUTES, MAX_RETRY_COUNT, OUTBOX_POLL_INTERVAL_SECONDS, LOG_FILE

### Changed
- Go 모듈 경로를 github.com/GrapeInTheTree/claude-cowork-telegram으로 변경
- 메시지 ID를 UnixMilli 기반으로 변경 (충돌 방지)
- inbox.json 구조에 retry_count, last_error, telegram_message_id 필드 추가

## [0.1.0] - 2026-03-16

### Added
- 텔레그램 봇 초기 구현
- 텔레그램 메시지 수신 → inbox.json 저장 (pending)
- outbox.json 10초 폴링 → done 항목 텔레그램 전송 → sent 변경
- TELEGRAM_CHAT_ID 기반 메시지 필터링
- .env 환경변수 로드
- .env.example 템플릿
