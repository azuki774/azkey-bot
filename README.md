# azkey-bot

azkey.azuki.blue 用 bot の基盤です。現在は設定を読み込み、起動と停止の
ライフサイクルを確認するところまでを実装しています。

## 必要環境

- Go 1.25 以降
- Misskey の URL、トークン、ルールファイルを環境変数で指定

`.env` は自動では読み込みません。シェルや実行環境から明示的に環境変数を
渡してください。

```sh
export MISSKEY_BASE_URL='https://misskey.example.invalid'
export MISSKEY_TOKEN='replace-with-a-local-token'
export RULES_FILE='./rules.example.json'
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

サンプルは `.env.example` と `rules.example.json` にあります。実際の
トークンやローカル設定はリポジトリへ保存しないでください。

## 起動・停止

```sh
mkdir -p ./bin
go build -o ./bin/azkey-bot ./cmd/azkey-bot
./bin/azkey-bot
```

ビルドしたプロセスへ `Ctrl-C` または `SIGTERM` を送ると正常に停止します。
現段階では空のルールを読み込んでも Misskey への通信や認証確認などの外部
操作は行いません。

## 構成

リポジトリは単一の Go モジュール（`github.com/azuki774/azkey-bot`）と、
単一プロセスのコマンド（`cmd/azkey-bot`）で構成します。

- `internal/config`: 環境変数とルール JSON の読み込み・検証
- `internal/domain`: 共有する最小限の業務値
- `internal/misskey`: 認証情報を非公開で保持する HTTP クライアントの準備
- `internal/polling`: キャンセル可能なポーリングのライフサイクル
- `internal/bot`: ポーリングを受け取る実行ライフサイクル
- `internal/repository/memory`: 将来の揮発性 `UserState` 保存領域の置き場所

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
旧 Docker イメージ公開 workflow と Python lint workflow は
削除し、現段階ではイメージ公開を行いません。

## 今後の範囲

- メモリリポジトリの `UserState`、`Get`、`Put` は issue #9 で定義します。
- ルールの業務スキーマは issue #10 で定義します。
- Misskey エンドポイントと認証付き操作は issue #11 で定義します。
- ポーリングとフォロワー同期は issue #12、#13 で定義します。
