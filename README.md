# azkey-bot

Misskey 向けのチェックイン bot `azkey-roumu-bot` です。

## 機能

- フォロワーの公開投稿をユーザーごとに定期取得し、本文のワード判定でチェックインを記録します。
  引用本文・リプライも対象です。本文なしリノート、bot・チャンネルの投稿は除外します。
- 設定した日付区切りで1日1回、累計日数・連続日数・最終日時を保存してリアクションします。
- bot 宛ての公開メンションに日数を返信します。照会は bot がフォローしているユーザーが対象です。
- フォロワーをフォローし、フォロワーではない相手へのフォローを解除します。

## 設定・起動

Go の必要バージョンは [`go.mod`](go.mod) を参照してください。

```sh
export MISSKEY_BASE_URL='https://misskey.example.invalid'
export MISSKEY_TOKEN='replace-with-a-local-token'
go run ./cmd/azkey-roumu-bot
```

トークンには `read:account`、`write:notes`、`write:following`、`write:reactions` が必要です。
`.env` は自動では読み込みません。停止は `Ctrl-C` または `SIGTERM` で行います。

| 設定 | 変更箇所 |
| --- | --- |
| 日付の区切り時刻・反応ワード | [`internal/domain/checkin_rules.go`](internal/domain/checkin_rules.go)（変更後にビルド・再デプロイ） |
| リアクション | [`DefaultCheckInSettings`](internal/roumu/bot/checkins.go)（変更後にビルド・再デプロイ） |
| 接続先・取得間隔・API 制限・ログ | [`.env.example`](.env.example) を参考に環境変数で指定 |

## 保存と再試行

- 保存と取得位置はオンメモリで、再起動すると消失します。過去や停止中の投稿は遡りません。
- 同一ユーザーの更新を直列化し、保存済みの日付以前の投稿は加算・反応しません。
- 読込・保存失敗は再読込から再試行します。保存結果が不明でも、保存済みなら加算・送信を省略します。
- リアクション・返信の送信失敗や結果不明では自動再送しません。保存後の送信失敗でも記録は維持します。
- 認証失敗は終了し、レート制限では待機します。関係一覧の取得失敗時は対象を維持し、フォロー変更を見送ります。

## 開発

```sh
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
go test -race ./...
go build ./...
```

CI でも上記と gofmt を確認します。
