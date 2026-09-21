# azkey-bot

`azkey-bot` は複数の bot を収めるリポジトリです。現在は最初の bot である
`azkey-roumu-bot` を一つのプロセスで動かし、設定の読み込みと起動・停止の
ライフサイクルを確認するところまでを実装しています。

## 必要環境

- Go 1.25 以降
- Misskey の URL、トークン、ルールファイルを環境変数で指定

`.env` は自動では読み込みません。シェルや実行環境から明示的に環境変数を
渡してください。

```sh
export MISSKEY_BASE_URL='https://misskey.example.invalid'
export MISSKEY_TOKEN='replace-with-a-local-token'
export RULES_FILE='./configs/azkey-roumu-bot/rules.example.json'
```

`MISSKEY_BASE_URL`、`MISSKEY_TOKEN`、`RULES_FILE` はすべて必須です。未指定や
空の値、URL の不備、読み込めないルールファイルは起動前に拒否します。

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
現段階では空のルールを読み込んでも、実行中の poller は Misskey への通信を
開始しません。HTTP クライアントの利用可能な範囲と対象バージョンの確認内容は
[`docs/misskey-http.md`](docs/misskey-http.md) に記載しています。

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
フォロー作成、リアクション作成を一ページ単位で提供します。ポーリング、
フォロワー同期、保存領域の操作はまだ実行しません。

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
  `internal/misskey` に実装済みです。対象範囲は `docs/misskey-http.md` を参照してください。
- ポーリングとフォロワー同期は issue #12、#13 で、取得・定期実行を
  `internal/roumu/polling`、ユースケースを `internal/roumu/bot` に定義します。

Issue #14 では、複数の bot を複数モジュールやプロセスオーケストレーションに
分けずに収められるよう、これらの bot 固有のパスを `internal/roumu` 配下へ
調整しました。
