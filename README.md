# azkey-bot

Misskey 向け bot のリポジトリです。現在は `azkey-roumu-bot` を実装しています。

## 設定・起動

Go 1.25 以降が必要です。HTTP クライアントは公式 Misskey `2026.9.0` を対象としています。

```sh
export MISSKEY_BASE_URL='https://misskey.example.invalid'
export MISSKEY_TOKEN='replace-with-a-local-token'

go run ./cmd/azkey-roumu-bot
```

`MISSKEY_BASE_URL` と `MISSKEY_TOKEN` のみ必須です。設定は環境変数だけから読み込み、設定ファイルのマウントは不要です。
`.env` は自動では読み込みません。実際のトークンやローカル設定はリポジトリへ保存しないでください。
停止は `Ctrl-C` または `SIGTERM` で行います。

起動すると公開リプライへの返信処理が動きます。モードの指定は不要です。

### チェックイン回数の照会

`notes/mentions` を定期取得し、bot の投稿に対する公開の直接リプライに
公開で返信します。単なるメンション、
非公開投稿、チャンネル投稿、自分自身・bot アカウントの投稿は対象外です。

- bot が投稿者をフォローしていなければ「あなたはメンバーではありません」と返信します。
  片方向でも bot → 投稿者のフォローが成立していればメンバーです。申請中は含みません。
- メンバーには「連続チェックイン回数: XX連勤、チェックイン回数: XX 日」と返信します。
  未登録の場合は両方 0 です。照会では記録を更新しません。
- 日付は JST 05:00 区切りです。最終チェックインが前日なら連続日数を維持し、
  丸1日チェックインしなかった時点で表示上の連続日数を 0 にします。累計日数は維持します。
- 保存項目は累計日数・連続日数・最終チェックイン日時です。保存はオンメモリで、再起動で消失します。
  記録を加算する処理は #10 で対応するため、現時点の実行ではメンバーへの返答は 0 になります。
- 起動時刻以降の投稿を処理し、過去や停止中の投稿には返信しません。
  接続先に遅れて届いた古い投稿の取得は保証しません。
- 読み取り・保存データ参照の失敗は取得位置を進めず、待機して再試行します。
  返信は全体で最低15秒間隔です。送信失敗・結果不明の場合も同じ投稿を自動再送しません。
  認証失敗は終了し、レート制限時は待機します。返信元の `localOnly` を引き継ぎます。

トークンには `read:account` と `write:notes` の権限が必要です。
`POLL_INTERVAL`、`POLL_RATE_PER_SECOND`、`POLL_PAGE_LIMIT`、`POLL_MAX_PAGES_PER_TURN`、
`POLL_BACKOFF_BASE`、`POLL_BACKOFF_MAX` で取得処理を調整できます。処理は直列です。

確認間隔は `POLL_INTERVAL`（既定 `1m`）で変更できます。
API リクエストは既定で毎秒2件に制限します。設定例は [`.env.example`](.env.example) を参照してください。
起動時刻を取得の基準にするため、実行環境の時計を同期してください。

## 開発

- `cmd/azkey-roumu-bot`: 起動と依存関係の組み立て
- `internal/misskey`・`internal/domain`: 共有の HTTP クライアントとデータ型
- `internal/roumu/bot`: bot の起動管理とリプライ照会処理
- `internal/roumu/repository/memory`: ユーザー状態のオンメモリ保存

```sh
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
go test -race ./...
go build ./...
```

CI でも上記と gofmt の確認を行います。
ユーザー状態の保存に関する Issue は [#9](https://github.com/azuki774/azkey-bot/issues/9)、
反応処理は [#10](https://github.com/azuki774/azkey-bot/issues/10)、
自動フォロー返しは [#13](https://github.com/azuki774/azkey-bot/issues/13) で扱います。
