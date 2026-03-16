# Cowork Telegram Bot

## 개요

Claude Cowork와 텔레그램을 연결하는 파일 기반 브릿지 봇.
맥북에서 실행 중인 Claude Cowork에게 텔레그램으로 작업을 지시하고
결과를 텔레그램으로 받아볼 수 있다.

## 아키텍처

```
핸드폰 텔레그램
    ↕
텔레그램 서버 (HTTPS Long Polling)
    ↕
Go 봇 (맥북 로컬 실행)
    ↕ 파일 읽기/쓰기 + Lock
inbox.json / outbox.json
    ↕ 1분마다 폴링
Claude Cowork Scheduled Task
```

## 파일 구조

```
claude-cowork-telegram/
├── main.go          # 엔트리포인트, 설정, 로거, graceful shutdown
├── model.go         # 데이터 타입 및 상태 상수
├── store.go         # JSON 파일 I/O, Mutex, Lock 파일 관리
├── bot.go           # 텔레그램 핸들러, 커맨드, 워커 goroutine
├── go.mod
├── go.sum
├── .env             # 환경변수 (git 제외)
├── .env.example     # 환경변수 템플릿
├── inbox.json       # 텔레그램 → Cowork 메시지 큐
├── outbox.json      # Cowork → 텔레그램 결과 큐
├── inbox.lock       # 동시 접근 방지 락 파일
├── bot.log          # 봇 실행 로그
└── README.md
```

## 메시지 상태 흐름

```
pending → processing → done → sent
                ↓
              error (최대 3회 retry 후 텔레그램 알림)
```

| 상태 | 설명 |
|---|---|
| pending | Go 봇이 기록, Cowork 처리 대기 중 |
| processing | Cowork가 현재 처리 중 |
| done | Cowork 처리 완료, 텔레그램 전송 대기 |
| sent | 텔레그램 전송 완료 |
| error | 처리 실패 (retry_count 확인) |

## inbox.json 구조

```json
{
  "messages": [
    {
      "id": "msg_1234567890",
      "text": "Downloads 폴더 정리해줘",
      "status": "pending",
      "timestamp": "2026-03-16T14:00:00Z",
      "retry_count": 0,
      "last_error": "",
      "telegram_message_id": 42
    }
  ]
}
```

## Lock 메커니즘

동시 접근 문제 방지를 위해 두 단계 Lock 사용:

1. **Go 봇 레벨**: `sync.Mutex`로 파일 접근 직렬화
2. **Cowork 레벨**: `inbox.lock` 파일로 스케줄 중복 실행 방지

```
Cowork 실행 시작
    → inbox.lock 존재 확인
    → 5분 이상 오래된 lock이면 stale로 판단 후 삭제
    → lock 없으면 inbox.lock 생성 (timestamp 기록)
    → 작업 수행
    → inbox.lock 삭제
```

## 설치 및 실행

### 사전 요구사항

- Go 1.21+
- Claude Desktop (Cowork 포함)
- 텔레그램 계정

### 설치

```bash
git clone git@github.com:GrapeInTheTree/claude-cowork-telegram.git
cd claude-cowork-telegram
go mod download
```

### 환경변수 설정

```bash
cp .env.example .env
nano .env
```

```env
TELEGRAM_TOKEN=your_bot_token
TELEGRAM_CHAT_ID=your_chat_id
INBOX_PATH=./inbox.json
OUTBOX_PATH=./outbox.json
LOCK_TIMEOUT_MINUTES=5
MAX_RETRY_COUNT=3
OUTBOX_POLL_INTERVAL_SECONDS=10
LOG_FILE=./bot.log
```

### 실행

```bash
# 개발
go run .

# 빌드 후 실행
go build -o cowork-bot
./cowork-bot
```

### 맥북 시작 시 자동 실행 (launchd)

```bash
# ~/Library/LaunchAgents/com.cowork.telegram.plist 생성
# 맥북 재시작 시 자동으로 봇 실행됨
```

## 텔레그램 특수 명령어

| 명령어 | 설명 |
|---|---|
| `/status` | 현재 pending/processing 메시지 수 + 마지막 실행 시간 |
| `/clear` | done/sent 처리된 메시지 정리 |
| `/retry` | error 상태 메시지 강제 재시도 |

## Cowork 스케줄 태스크 설정

- **이름**: cowork-bridge-inbox
- **주기**: 1분마다 (`*/1 * * * *`)
- **폴더**: `/Users/<username>/claude-cowork-telegram`
- **동작**: inbox.json polling → 작업 수행 → outbox.json 기록

## 보안

- `TELEGRAM_CHAT_ID`에 등록된 본인 ID 외 모든 메시지 무시
- `.env` 파일은 절대 git에 커밋하지 말 것
- 봇 토큰 유출 시 BotFather에서 즉시 revoke

## 한계 및 주의사항

- 맥북과 Claude Desktop이 켜져 있어야 동작
- 응답 시간: 최대 1분 (Cowork 스케줄 주기)
- 절전 모드 시 동작 중단 → 시스템 환경설정에서 절전 비활성화 권장
- Cowork usage를 소비하므로 Pro/Max 플랜 사용량 모니터링 권장
