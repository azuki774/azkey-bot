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

- 取得位置はオンメモリで、再起動すると消失します。過去や停止中の投稿は遡りません。
- チェックイン状態は既定でオンメモリです。`KVS_BACKEND=valkey` で累計日数・連続日数・最終日時を永続化できます。
- 同一ユーザーの更新を直列化し、保存済みの日付以前の投稿は加算・反応しません。
- 読込・保存失敗は再読込から再試行します。保存結果が不明でも、保存済みなら加算・送信を省略します。
- リアクション・返信の送信失敗や結果不明では自動再送しません。保存後の送信失敗でも記録は維持します。
- 認証失敗は終了し、レート制限では待機します。関係一覧の取得失敗時は対象を維持し、フォロー変更を見送ります。

### Valkeyへの保存

`.env.example` の `KVS_*` を設定します。`KVS_NAMESPACE` は環境・Misskey接続先・Botごとに
異なる固定値にし、再デプロイでは維持してください。`KVS_URL` はポート必須で、
`rediss://` が証明書検証付きTLS、`redis://` が平文です。認証情報をURLには含めません。
パスワードは `KVS_PASSWORD` または `KVS_PASSWORD_FILE` で注入します（併用不可、ファイル末尾の改行は除去）。
ユーザー名は `KVS_USERNAME`、DB番号は `KVS_DB`（既定0）です。

- チェックイン処理と照会処理で同じRepositoryを共有します。起動時の接続・認証失敗は起動エラーになります。
- 1ユーザー1キーのJSONで、TTLは付けません。通常投稿・取得進捗は保存せず、フォロー解除でも記録は削除しません。
- キーは `roumu:v1:{namespace}:users:{userId}`。各成分はURL-safe Base64（パディングなし）で符号化します。
  値は `version`、`checkInDays`、`consecutiveDays`、`lastCheckInAt` です。
- 同じnamespaceは単一Botプロセスで使います。Valkey側はAOF等の永続化とバックアップを設定し、
  保存データがメモリ上限による削除の対象にならないようにしてください。
- 読み込み障害や破損値を未登録として扱いません。保存結果が不明な場合は既存の再読込処理で確認します。

デバッグ時は `valkey-cli --tls -h HOST -p 6379 --user USER --askpass` で接続し、
`SCAN 0 MATCH roumu:v1:*:users:* COUNT 100` と `GET <key>` で値を確認できます。
SCANは返されたカーソルが0になるまで続けます。DB番号を変えた場合は接続時に `-n DB番号` も指定します。

## 開発

```sh
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
go test -race ./...
go build ./...
```

CI でも上記と gofmt を確認します。
