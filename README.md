# azkey-bot

Misskey 向け bot のリポジトリです。現在は `azkey-roumu-bot` を実装しています。

## ノートの取得方式

**bot と相互フォローしているユーザー（FF関係）のノートを、ユーザーごとに見て回る方式です。**
タイムラインをまとめて取得したり、WebSocket で流れてくるノートを受信したりはしません。

1. bot のフォロワー一覧とフォロー中一覧を取得し、両方にいるユーザーを監視対象にする
   （既定で5分ごと）。
2. 対象ユーザーの `users/notes` を呼び、前回確認した位置より新しいノートを取得する
   （既定で各ユーザー約60秒ごと）。
3. 公開ノートだけを処理対象にする。公開リプライ・リノートも取得し、bot 自身と
   チャンネルのノートは除外する。

片方向のフォローや、フォロー申請中で相互フォローが成立していない相手は対象外です。
完全に1人ずつ順番に回るのではなく、
アクセス時刻を分散し、既定で最大2件を並行取得します。読み取り API 全体を毎秒2リクエストに
制限するため、対象人数・投稿数・通信状況によって確認間隔は延びます。

**現在は観察モード（`observe`）のみです。** 対象ノートの ID と投稿者 ID をログに記録し、
リアクション送信・自動フォロー返しは行いません。

### 運用上の注意

- 初回・新規対象者の追加時は過去のノートを処理せず、最新位置から監視を始めます。
- 取得位置はメモリ保持です。再起動で失われ、停止中のノートは遡りません。
- 両方の一覧を最後まで取得できた場合だけ監視対象を更新します。どちらかの取得に失敗した場合は
  前回の対象を維持し、相互フォローの解除は次回の同期成功時に反映します。
- 接続先サーバーに見えるノートのみが対象です。リモートの全ノートや、取得位置より古い
  遅延到着ノートの取得は保証しません。
- 初回にノートがないユーザーは時刻を基準にするため、実行環境の時計を同期してください。

## 設定・起動

Go 1.25 以降が必要です。HTTP クライアントは公式 Misskey `2026.9.0` を対象としています。

```sh
export MISSKEY_BASE_URL='https://misskey.example.invalid'
export MISSKEY_TOKEN='replace-with-a-local-token'
export RULES_FILE='./configs/azkey-roumu-bot/rules.example.json'
export POLLING_MODE='observe'

go run ./cmd/azkey-roumu-bot
```

`MISSKEY_BASE_URL`、`MISSKEY_TOKEN`、`RULES_FILE` は必須です。
`.env` は自動では読み込みません。実際のトークンやローカル設定はリポジトリへ保存しないでください。
停止は `Ctrl-C` または `SIGTERM` で行います。

反応ルールは未実装のため、ルールファイルには上記のサンプル（`version: 1`、空の `rules`）を使ってください。
`POLLING_MODE` は未指定でも `observe` になり、他の値は受け付けません。

確認間隔は `POLL_INTERVAL`（既定 `1m`）、相互フォローの同期間隔は
`FOLLOWER_SYNC_INTERVAL`（既定 `5m`）で変更できます。
並行数・レート制限などの設定例は [`.env.example`](.env.example) を参照してください。

## 開発

- `cmd/azkey-roumu-bot`: 起動と依存関係の組み立て
- `internal/misskey`・`internal/domain`: 共有の HTTP クライアントとデータ型
- `internal/roumu`: bot 固有の処理。ノート取得は `polling`、処理側への受渡しは `NoteHandler`

```sh
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
go test -race ./...
go build ./...
```

CI でも上記と gofmt の確認を行います。
ユーザー状態の保存は [#9](https://github.com/azuki774/azkey-bot/issues/9)、
反応処理は [#10](https://github.com/azuki774/azkey-bot/issues/10)、
自動フォロー返しは [#13](https://github.com/azuki774/azkey-bot/issues/13) で扱います。
