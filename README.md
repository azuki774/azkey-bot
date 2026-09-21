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
現段階では空のルールを読み込んでも Misskey への通信や認証確認などの外部
操作は行いません。

## Docker

Docker イメージは Alpine ベースの実行環境に静的にビルドした
`azkey-roumu-bot` と CA 証明書を配置し、root ではないユーザーで起動します。
トークンやルールファイルはイメージへ埋め込まず、起動時に環境変数と
読み取り専用の外部マウントで渡してください。

```sh
docker build -t ghcr.io/azuki774/azkey-bot-roumu:local .
docker run --rm \
  --env MISSKEY_BASE_URL='https://misskey.example.invalid' \
  --env MISSKEY_TOKEN \
  --env RULES_FILE=/config/rules.json \
  --volume "$PWD/configs/azkey-roumu-bot/rules.example.json:/config/rules.json:ro" \
  ghcr.io/azuki774/azkey-bot-roumu:local
```

GitHub Actions は pull request ではイメージをビルドして起動・`SIGTERM` 停止を
確認するだけで、レジストリへのログインや push は行いません。`master` への
push では `ghcr.io/azuki774/azkey-bot-roumu:<コミット SHA 先頭 7 文字>` を公開し、
`v1.2.3` のような有効な SemVer タグでは先頭の `v` を除いた
`ghcr.io/azuki774/azkey-bot-roumu:1.2.3` を公開します。SemVer の build metadata
（`+build` など）は Docker タグに使えないため受け付けず、`latest` や major/minor
の別名タグも発行しません。

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
    ├── misskey/                     # bot 間で共有する Misskey クライアント境界
    └── roumu/                       # azkey-roumu-bot 固有の処理
        ├── bot/                     # 実行ライフサイクル
        ├── config/                  # 環境変数とルール JSON の読み込み・検証
        ├── domain/                  # azkey-roumu-bot の業務値
        ├── polling/                 # キャンセル可能なポーリングのライフサイクル
        └── repository/memory/       # 揮発性 UserState 保存領域の置き場所
```

- `internal/misskey`: 認証情報を非公開で保持する共有 HTTP クライアントの準備
- `internal/roumu`: `azkey-roumu-bot` に固有の設定、業務値、実行処理

Misskey のエンドポイント呼び出し、ポーリング、フォロワー同期はまだなく、
この基盤から外部通信は発生しません。メモリ保存領域のデータ構造と操作も
未定義です。

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
コンテナ workflow の publish job は、同じ workflow の Go の test、vet、build と
コンテナの smoke test が成功した場合だけ実行されます。

## 今後の範囲

- ユーザー状態と repository は issue #9 で、型を `internal/roumu/domain`、
  利用側 interface を `internal/roumu/bot`、保存実装を
  `internal/roumu/repository/memory` に定義します。
- ルールの業務スキーマは issue #10 で `internal/roumu/domain` に定義します。
- Misskey エンドポイントと認証付き操作は issue #11 で共有の
  `internal/misskey` に定義します。
- ポーリングとフォロワー同期は issue #12、#13 で、取得・定期実行を
  `internal/roumu/polling`、ユースケースを `internal/roumu/bot` に定義します。

Issue #14 では、複数の bot を複数モジュールやプロセスオーケストレーションに
分けずに収められるよう、これらの bot 固有のパスを `internal/roumu` 配下へ
調整しました。
