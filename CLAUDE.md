# CLAUDE.md

이 파일은 Claude Code가 이 프로젝트를 이해하고 작업할 때 참고하는 컨텍스트 문서입니다.

## 프로젝트 개요

Claude Cowork와 텔레그램을 연결하는 파일 기반 브릿지 봇 (Go).
맥북 로컬에서 실행되며, 텔레그램 메시지를 inbox.json에 기록하고
Cowork가 처리한 결과를 outbox.json에서 읽어 텔레그램으로 전송한다.

## 아키텍처

```
텔레그램 ↔ Go 봇 ↔ inbox.json / outbox.json ↔ Claude Cowork (1분 스케줄)
```

- **inbox.json**: 텔레그램 → Cowork 방향. 봇이 쓰고 Cowork가 읽는다.
- **outbox.json**: Cowork → 텔레그램 방향. Cowork가 쓰고 봇이 읽는다.
- **inbox.lock**: 동시 접근 방지. Go sync.Mutex(프로세스 내) + lock file(프로세스 간).

## 파일 구조

| 파일 | 역할 |
|---|---|
| `main.go` | 엔트리포인트, Config, 로거, graceful shutdown |
| `model.go` | 데이터 타입 (InboxMessage, OutboxMessage) 및 상태 상수 |
| `store.go` | JSON 파일 I/O, Mutex, lock file 메커니즘 |
| `bot.go` | 텔레그램 핸들러, 커맨드 (/status, /clear, /retry), outbox 폴러 |

## 빌드 및 실행

```bash
go build ./...     # 빌드
go run .           # 실행 (멀티파일이므로 go run main.go 아닌 go run .)
```

## 주요 설계 결정

### outbox 전용 전송
- 텔레그램 결과 전송은 outbox.json 폴링으로만 수행
- inbox.json의 "done" 상태는 Cowork 상태 추적용이며 텔레그램 전송을 트리거하지 않음
- 이유: inbox와 outbox 동시 폴링 시 중복 전송 및 empty result 문제 발생

### outbox 유연 파싱
- Cowork가 `{"messages":[...]}` 또는 `[...]` 배열 형식으로 쓸 수 있음
- store.go의 readOutbox가 두 형식 모두 처리

### empty result 스킵
- outbox 메시지의 result가 비어있으면 전송하지 않고 대기
- Cowork가 2단계로 쓸 때 (status 먼저, result 나중) 빈 메시지 전송 방지

## 주의사항

- 봇 인스턴스는 반드시 1개만 실행할 것. 텔레그램 Long Polling은 동시 접속 불가.
- `.env` 파일에 실제 토큰이 있으므로 절대 커밋하지 말 것
- inbox.json, outbox.json에 대화 내용 + 개인정보 포함 가능 → 커밋 금지
- bot.log, *.lock 파일도 커밋 대상 아님

## 환경변수

| 변수 | 기본값 | 설명 |
|---|---|---|
| `TELEGRAM_TOKEN` | (필수) | 봇 토큰 |
| `TELEGRAM_CHAT_ID` | (필수) | 허용할 채팅 ID |
| `INBOX_PATH` | `./inbox.json` | 수신 메시지 파일 |
| `OUTBOX_PATH` | `./outbox.json` | 송신 결과 파일 |
| `LOCK_TIMEOUT_MINUTES` | `5` | stale lock 판단 시간 |
| `MAX_RETRY_COUNT` | `3` | 에러 메시지 최대 재시도 |
| `OUTBOX_POLL_INTERVAL_SECONDS` | `10` | outbox 폴링 주기 |
| `LOG_FILE` | `./bot.log` | 로그 파일 경로 |
