# CLAUDE.md

이 파일은 Claude Code가 이 프로젝트를 이해하고 작업할 때 참고하는 컨텍스트 문서입니다.

## 프로젝트 개요

텔레그램 메시지를 받아 Claude Code CLI로 즉시 처리하고 결과를 텔레그램으로 전송하는 Go 봇.
맥북 로컬에서 실행되며, Claude Cowork 스케줄 없이 메시지 도착 즉시 `claude -p`를 호출한다.

## 아키텍처

```
텔레그램 → Go 봇 → inbox.json (pending)
                 → Worker → claude -p (CLI subprocess)
                          → 텔레그램 직접 전송 + outbox.json (audit)
```

- **Worker**: 메시지 큐에서 하나씩 꺼내 `claude -p`로 처리. 단일 goroutine으로 순차 실행.
- **PollPending**: 30초마다 inbox에서 pending 메시지 확인 (retry/recovery fallback).
- **PollOutbox**: outbox.json에서 done 메시지 전송 (레거시 Cowork 호환 + fallback).

## 파일 구조

| 파일 | 역할 |
|---|---|
| `main.go` | 엔트리포인트, Config, 로거, graceful shutdown, 전체 와이어링 |
| `model.go` | 데이터 타입 (InboxMessage, OutboxMessage) 및 상태 상수 |
| `store.go` | JSON 파일 I/O, Mutex, lock file 메커니즘 |
| `bot.go` | 텔레그램 핸들러, 커맨드, outbox 폴러, retry 프로세서 |
| `claude.go` | Claude CLI executor (subprocess 관리, 타임아웃) |
| `worker.go` | 메시지 처리 워커 (큐, dedup, pending poll, stale recovery) |

## 빌드 및 실행

```bash
go build ./...     # 빌드
go run .           # 실행
```

## 주요 설계 결정

### Claude CLI 직접 호출 (v0.4.0~)
- Cowork 1분 스케줄 대신 메시지 도착 즉시 `claude -p` subprocess 실행
- 응답 시간: 수초 (스케줄 대기 없음)
- Usage: 메시지당 1회만 소비 (idle 시 소비 없음)
- MCP 서버(Slack, Notion, Gmail 등) 동일하게 사용 가능

### Worker 패턴
- 단일 worker goroutine이 큐에서 순차 처리 (동시 CLI 호출 방지)
- sync.Map으로 in-flight 메시지 중복 방지
- 텔레그램 직접 전송 성공 시 outbox에 "sent"로 기록 (audit trail)
- 전송 실패 시 outbox에 "done"으로 기록 → outbox poller가 재시도

### Stale recovery
- 봇 시작 시 "processing" 상태 메시지를 "pending"으로 복구
- 비정상 종료로 중단된 메시지 자동 재처리

### outbox 유연 파싱
- `{"messages":[...]}` 또는 `[...]` 배열 형식 모두 처리
- 레거시 Cowork와의 호환성 유지

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
| `CLAUDE_CLI_PATH` | `claude` | Claude CLI 바이너리 경로 |
| `CLAUDE_WORK_DIR` | `.` | CLI 작업 디렉토리 |
| `CLAUDE_TIMEOUT_SECONDS` | `120` | CLI 실행 타임아웃 |
| `CLAUDE_SYSTEM_PROMPT` | (없음) | 커스텀 시스템 프롬프트 |
| `CLAUDE_MODEL` | (없음) | 모델 지정 (예: sonnet, opus) |
| `WORKER_QUEUE_SIZE` | `100` | 워커 큐 크기 |
