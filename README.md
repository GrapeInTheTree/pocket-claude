# cowork-telegram-bot

텔레그램 메시지를 inbox.json으로 수신하고, outbox.json의 결과를 텔레그램으로 전송하는 봇.

## 설정

```bash
cp .env.example .env
# .env 파일에 TELEGRAM_TOKEN, TELEGRAM_CHAT_ID 입력
```

## 실행

```bash
go run main.go
```

## 동작 방식

1. **수신**: 텔레그램 메시지 → `inbox.json`의 `messages` 배열에 `status: "pending"`로 추가
2. **송신**: `outbox.json`을 10초마다 폴링 → `status: "done"` 항목의 `result`를 텔레그램 전송 → `status: "sent"`로 변경

## 메시지 형식

### inbox.json
```json
{
  "messages": [
    {
      "id": "msg_1710000000",
      "text": "메시지 내용",
      "status": "pending",
      "timestamp": "2026-03-16T05:00:00Z"
    }
  ]
}
```

### outbox.json
```json
{
  "messages": [
    {
      "id": "msg_1710000000",
      "text": "원본 메시지",
      "status": "done",
      "result": "처리 결과",
      "timestamp": "2026-03-16T05:00:00Z"
    }
  ]
}
```
