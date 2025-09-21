# Docker での実行方法

azkey-bot-roumu を Docker で実行するためのガイドです。

## 事前準備

1. 環境変数ファイルを作成:
```bash
cp .env.example .env
# .env ファイルを編集して実際のトークンを設定
```

2. データディレクトリを作成:
```bash
mkdir -p data
```

## 基本的な使用方法

### 単発実行

```bash
# イメージをビルド
docker build -t azkey-bot-roumu .

# ステータス確認
docker run --rm --env-file .env -v $(pwd)/data:/app/data azkey-bot-roumu status

# フォローバック実行
docker run --rm --env-file .env -v $(pwd)/data:/app/data azkey-bot-roumu follow

# タイムラインチェック実行
docker run --rm --env-file .env -v $(pwd)/data:/app/data azkey-bot-roumu check

# デバッグ: 特定ユーザーの打刻
docker run --rm --env-file .env -v $(pwd)/data:/app/data azkey-bot-roumu dakoku --user-id USER_ID

# タイムライン表示
docker run --rm --env-file .env -v $(pwd)/data:/app/data azkey-bot-roumu timeline --limit 5
```

### Docker Compose での実行

```bash
# サービス起動
docker-compose up -d

# ログ確認
docker-compose logs -f azkey-bot-roumu

# 特定サービスのログ確認
docker-compose logs -f follow-service
docker-compose logs -f check-service

# サービス停止
docker-compose down
```

## 自動実行（スケジューラー版）

定期実行が組み込まれたバージョンを使用する場合:

```bash
# スケジューラー版をビルド
docker build -f Dockerfile.scheduler -t azkey-bot-roumu:scheduler .

# 自動実行開始（follow: 1時間毎、check: 5分毎）
docker run -d --name azkey-bot-scheduler \
  --env-file .env \
  -v $(pwd)/data:/app/data \
  azkey-bot-roumu:scheduler

# ログ確認
docker logs -f azkey-bot-scheduler

# 停止
docker stop azkey-bot-scheduler
docker rm azkey-bot-scheduler
```

## ログの確認

構造化ログが出力されるため、以下のようにフィルタリング可能:

```bash
# フォローバック関連のログのみ
docker logs azkey-bot-scheduler 2>&1 | grep "action=follow"

# 打刻成功のログのみ
docker logs azkey-bot-scheduler 2>&1 | grep "action=checkin_success"

# エラーログのみ
docker logs azkey-bot-scheduler 2>&1 | grep "level=ERROR"
```

## データの永続化

- CSV ファイル（`roumu.csv`）は `/app/data` ディレクトリに保存されます
- Docker ボリュームまたはホストディレクトリをマウントして永続化してください
- バックアップは `data/` ディレクトリをコピーするだけです

## トラブルシューティング

### 環境変数が設定されていない場合
```
設定エラー: Environment variable 'i' is not set
```
→ `.env` ファイルの `MISSKEY_TOKEN` を確認

### API エラーが発生する場合
```
action=follow_error error="HTTP 401: ..."
```
→ Misskey トークンの権限を確認

### CSV ファイルへのアクセスエラー
```
PermissionError: [Errno 13] Permission denied: 'roumu.csv'
```
→ data ディレクトリの権限を確認: `chmod 755 data`

## 本格運用

本格的な運用では以下を検討してください:

1. **Kubernetes CronJob** での定期実行
2. **ログ集約システム**（Fluentd、Logstash等）での構造化ログ処理
3. **監視システム**（Prometheus、Grafana等）でのメトリクス収集
4. **バックアップ戦略** でのデータ保護
5. **セキュリティ**（シークレット管理、ネットワーク分離等）