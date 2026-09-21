# azkey-bot

`azkey-bot` は複数の bot を収めるリポジトリです。現在は最初の bot である
`azkey-roumu-bot` を一つのプロセスで動かし、フォロワーの公開投稿を読み取る
ポーリングと起動・停止のライフサイクルを実装しています。

## 必要環境

- Go 1.25 以降
- Misskey の URL、トークン、ルールファイルを環境変数で指定

`.env` は自動では読み込みません。シェルや実行環境から明示的に環境変数を
渡してください。

```sh
export MISSKEY_BASE_URL='https://misskey.example.invalid'
export MISSKEY_TOKEN='replace-with-a-local-token'
export RULES_FILE='./configs/azkey-roumu-bot/rules.example.json'
export POLLING_MODE='observe'
```

`MISSKEY_BASE_URL`、`MISSKEY_TOKEN`、`RULES_FILE` はすべて必須です。未指定や
空の値、URL の不備、読み込めないルールファイルは起動前に拒否します。
`POLLING_MODE` は現在 `observe` だけを受け付けます。未指定時も安全な観察モード
になりますが、運用時は明示的に指定してください。観察モードは対象投稿の ID と
投稿者 ID だけをログへ出力し、Misskey への書き込みは行いません。

ポーリングの主な設定には次の既定値があります。値は環境変数で上書きでき、期間は
`time.ParseDuration` の形式です。

| 環境変数 | 既定値 | 内容 |
| --- | ---: | --- |
| `POLL_INTERVAL` | `1m` | 各フォロワーの投稿確認間隔 |
| `FOLLOWER_SYNC_INTERVAL` | `5m` | フォロワー一覧の完全同期間隔 |
| `POLL_CONCURRENCY` | `2` | 同時に動かす読み取りワーカー数 |
| `POLL_RATE_PER_SECOND` / `POLL_RATE_BURST` | `2` / `1` | 全読み取り要求で共有する制限 |
| `POLL_PAGE_LIMIT` | `100` | 投稿・フォロワーの 1 ページ上限 |
| `POLL_MAX_PAGES_PER_TURN` | `5` | 1 フォロワーを 1 回に読む最大ページ数 |
| `POLL_DEDUP_LIMIT` / `POLL_DEDUP_TTL` | `10000` / `24h` | 全フォロワーで共有する成功処理済み ID の揮発キャッシュ |
| `POLL_STARTUP_SPREAD` | `1m` | 新規フォロワーの初回処理を分散する範囲 |

ルールファイルは現在、次の厳密な形式だけを受け付けます。`version` は `1`、
`rules` は空配列でなければなりません。追加のキー、欠落・`null`、後続 JSON、
`rules` の要素は拒否します。

```json
{
  "version": 1,
  "rules": []
}
```

サンプルは `.env.example` と `configs/azkey-roumu-bot/rules.example.json` にあります。実際の
トークンやローカル設定はリポジトリへ保存しないでください。

## 起動・停止

```sh
mkdir -p ./bin
go build -o ./bin/azkey-roumu-bot ./cmd/azkey-roumu-bot
./bin/azkey-roumu-bot
```

ビルドしたプロセスへ `Ctrl-C` または `SIGTERM` を送ると正常に停止します。
起動すると `/api/i` で自身の ID を確認し、`/api/users/followers` を全ページ取得
してフォロワー集合を同期します。完全な取得に成功したときだけ集合を置き換え、通信
失敗・不正応答・途中終了では直前の集合を保持します。フォロー作成やリアクション
作成などの書き込み操作は行いません。HTTP クライアントは公式 Misskey
`2026.9.0` を対象としています。

フォロワーごとの初回読み取りでは最新投稿を処理せず、位置だけを初期化します。
空の初回一覧では取得開始時刻を API のミリ秒精度へ切り捨てた境界として記録し、次回から 1 ミリ秒重ねて検索します。
境界より古い投稿は除外しますが、境界と同時刻の最初の新規投稿は除外しません。
この判定は実行環境と Misskey の時計が同期していることを前提にしています。

## 構成

リポジトリは単一の Go モジュール（`github.com/azuki774/azkey-bot`）で、現在は
一つの bot を一つのプロセスで実行します。共有の Misskey 境界と
`azkey-roumu-bot` 固有の処理は次のように分かれています。

```
.
├── cmd/
│   └── azkey-roumu-bot/             # azkey-roumu-bot のコマンド
├── configs/
│   └── azkey-roumu-bot/
│       └── rules.example.json       # azkey-roumu-bot の設定例
└── internal/
    ├── domain/                      # bot 間で共有する最小限の値とエラー
    ├── misskey/                     # bot 間で共有する Misskey HTTP クライアント
    └── roumu/                       # azkey-roumu-bot 固有の処理
        ├── bot/                     # 実行ライフサイクル
        ├── config/                  # 環境変数とルール JSON の読み込み・検証
        ├── domain/                  # azkey-roumu-bot の業務値
        ├── polling/                 # キャンセル可能なポーリングのライフサイクル
        └── repository/memory/       # 揮発性 UserState 保存領域の置き場所
```

- `internal/domain`: bot 間で渡す User、Following、Note と分類済みエラー
- `internal/misskey`: 認証情報を非公開で保持する共有 HTTP クライアント
- `internal/roumu`: `azkey-roumu-bot` に固有の設定、業務値、実行処理

Misskey クライアントは bot 自身、フォロワー・フォロー一覧、ユーザー投稿一覧、
フォロー作成、リアクション作成を提供します。一覧は一ページ単位の取得で、自動再試行
や暗黙のページ送りはありません。ポーリング層はフォロワー一覧とユーザー投稿一覧の
読み取りだけを使い、公開投稿・対象フォロワー本人の投稿・bot 自身ではない投稿だけを
`NoteHandler` へ渡します。`withReplies=true`、`withRenotes=true`、
`withChannelNotes=false` を明示し、renote を業務上どう扱うかは後続の処理側で決めます。

取得対象はこの Misskey サーバーから見える範囲の投稿だけです。remote の全投稿や、
遅れて到着した投稿・作成時刻を過去へ戻した投稿が、既存の位置より前にある場合は取得
できるとは限りません。位置と全フォロワー共有の重複キャッシュはメモリだけに保持するため、再起動すると
フォロワーごとに再び初期位置を作り、停止中の期間を遡って取得しません。公開投稿の
受け渡し先となる反応判定と書き込み処理は issue #10 で定義するため、現段階では観察
ログに限定しています。

## テスト

```sh
gofmt -w .
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
go test -race ./...
go build ./...
```

CI でも gofmt の確認、`go vet ./...`、Staticcheck、`go build ./...`、
`go test -race ./...` を実行します。Staticcheck は Go 1.25 対応の
2026.1（`v0.7.0`）に固定し、実行してもアプリの `go.mod` は変更しません。
GitHub Actions の参照は完全なコミット SHA に固定しています。
Go の検証は通常 CI で行い、コンテナ workflow はタグ判定・イメージビルド・公開を担当します。

## 今後の範囲

- ユーザー状態と repository は issue #9 で、型を `internal/roumu/domain`、
  利用側 interface を `internal/roumu/bot`、保存実装を
  `internal/roumu/repository/memory` に定義します。
- ルールの業務スキーマは issue #10 で `internal/roumu/domain` に定義します。
- Misskey エンドポイントと認証付き操作は issue #11 として共有の
  `internal/misskey` に実装済みです。自動再試行とリアクション削除は含みません。
- ポーリングとフォロワー同期は issue #12 の範囲として、取得・定期実行を
  `internal/roumu/polling` に定義しました。issue #13 の自動フォローはまだ実装していません。
  ユースケースの受け渡し境界は `NoteHandler` で、実際の反応処理は issue #10 の
  実装後に接続します。

Issue #14 では、複数の bot を複数モジュールやプロセスオーケストレーションに
分けずに収められるよう、これらの bot 固有のパスを `internal/roumu` 配下へ
調整しました。
