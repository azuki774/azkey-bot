# azkey-bot-roumu

azkey.azuki.blue Misskey サーバー向けのログインボーナス（ろうむ）ボットです。

## 概要

このボットは以下の機能を提供します：

- **フォローバック自動化**: フォローしてくれたユーザーを自動でフォローバック
- **キーワード監視**: タイムラインでログボ関連キーワードを監視し、自動打刻
- **打刻システム**: 連続打刻回数を記録し、CSV ファイルで管理
- **自動リアクション**: 打刻対象の投稿に 👍 リアクションを自動追加

## セットアップ

### 前提条件

- Python 3.13+
- uv パッケージマネージャー（推奨）または pip

### インストール

```bash
# リポジトリをクローン
git clone <repository-url>
cd azkey-bot-roumu

# 依存関係をインストール
uv install
# または
pip install -e .
```

### 環境変数設定

必要な環境変数を設定してください：

```bash
export i="your_misskey_access_token"
export OPENROUTER_API_KEY="your_openrouter_api_key"

# オプション: CSVファイル保存ディレクトリ（デフォルト: カレントディレクトリ）
export ROUMU_DATA_DIR="/path/to/data/directory"

# オプション: Misskeyサーバーエンドポイント（デフォルト: azkey.azuki.blue）
export MISSKEY_ENDPOINT="https://your-misskey-instance.example.com"
```

## 使用方法

### 基本的なコマンド

```bash
# ステータス確認
azkey-bot-roumu status

# フォローバック実行（手動）
azkey-bot-roumu follow

# タイムラインチェック・自動打刻（手動）
azkey-bot-roumu check

# 特定ユーザーの手動打刻（デバッグ用）
azkey-bot-roumu dakoku --user-id USER_ID

# タイムライン表示（デバッグ用）
azkey-bot-roumu timeline --limit 10

# 全ユーザーのカウントリセット
azkey-bot-roumu reset
```

### 自動実行

本格運用では定期実行を設定してください：

```bash
# crontab での設定例
# フォローバック: 1時間毎
0 * * * * /path/to/azkey-bot-roumu follow

# タイムラインチェック: 5分毎
*/5 * * * * /path/to/azkey-bot-roumu check
```

## Docker での実行

Docker を使用した実行方法については `DOCKER.md` を参照してください。

### 基本的な Docker 実行

```bash
# イメージをビルド（キャッシュなし）
make build

# 各コマンドの実行
make status     # ステータス確認
make follow     # フォローバック実行
make check      # タイムラインチェック・自動打刻
make reset      # 全ユーザーのカウントリセット

# データディレクトリの初期設定
make setup-data

# 一括セットアップ
make quick-start
```

## 監視キーワード

以下のキーワードが投稿に含まれている場合、自動打刻の対象となります：

- **ログインボーナス**
- **ログボ** 
- **打刻**

## データ管理

### roumu.csv ファイル

打刻データは `roumu.csv` ファイルに保存されます：

| フィールド | 説明 |
|-----------|------|
| user_id | Misskey ユーザーID |
| consecutive_count | 連続打刻回数 |
| total_count | 累計打刻回数 |
| last_checkin | 最後の打刻日時（ISO形式） |

### カウントリセット機能

`reset` コマンドは全ユーザーのカウントを以下のロジックでリセットします：

- **`last_checkin` が空の場合**: `consecutive_count` を 0 にリセット
- **`last_checkin` が空でない場合**: `last_checkin` を空文字列にクリア（再打刻可能状態）

```bash
# 全ユーザーのカウントリセット実行
azkey-bot-roumu reset
# または
make reset
```

**使用例:**
- 月次リセット: 毎月1日に実行して連続記録をリセット
- 再打刻許可: 特定の日に全ユーザーの再打刻を許可

### ⚠️ 重要な注意事項

**CSV ファイルの同時アクセスについて:**

現在の実装では `roumu.csv` ファイルに対する排他制御（ファイルロック）を行っていません。以下の点にご注意ください：

- **複数プロセスの同時実行は避けてください**
- `follow` と `check` コマンドを同時に実行するとデータ破損の可能性があります
- Docker での実行時も、複数のコンテナで同じ CSV ファイルに同時アクセスしないようにしてください
- 本格的な運用では SQLite や Redis などのデータベースへの移行を検討してください

**推奨事項:**
- cron で実行する場合は、実行間隔を調整して重複を避ける
- 複数インスタンスでの運用は避ける
- 定期的なバックアップを実施する

## ログ出力

本ボットは構造化ログを出力します。ログは以下の形式で出力されます：

```
action=check_start keywords="ログインボーナス,ログボ,打刻"
action=checkin_success user_id=abc123 username="user1" post_id=xyz789 consecutive_count=5 total_count=25
action=reaction_added post_id=xyz789 reaction=👍
action=reset_complete total_users=10 consecutive_count_reset=3 last_checkin_reset=7
```

### ログレベル

- `INFO`: 通常の処理状況
- `WARNING`: 警告（リアクション失敗など）
- `ERROR`: エラー（API エラーなど）

## 開発

### コード品質

```bash
# Ruff でのリント・フォーマット
uv run ruff check
uv run ruff check --fix
uv run ruff format
```

### 開発モードでの実行

```bash
# uv で実行
cd azkey-bot-roumu
uv run python -m azkey_bot_roumu.cli status
```

### GitHub Actions

コミット時に自動で Ruff による品質チェックが実行されます。

## API 権限

Misskey のアクセストークンには以下の権限が必要です：

- **読み取り権限**: 
  - タイムライン取得
  - ユーザー情報取得
  - フォロー・フォロワー情報取得
- **書き込み権限**:
  - フォロー実行
  - リアクション追加

## トラブルシューティング

### よくあるエラー

**環境変数エラー:**
```
設定エラー: Environment variable 'i' is not set
```
→ Misskey アクセストークンが設定されていません

**API エラー:**
```
HTTP 401: Unauthorized
```
→ アクセストークンが無効または権限不足です

**CSV エラー:**
```
PermissionError: [Errno 13] Permission denied: 'roumu.csv'
```
→ ファイルの権限を確認してください

### 対処方法

1. **環境変数の確認**: `.env` ファイルまたは `export` コマンドで設定
2. **権限の確認**: Misskey の設定画面でアクセストークンの権限を確認
3. **ファイル権限**: `chmod 644 roumu.csv` でファイル権限を修正
4. **ログの確認**: 構造化ログでエラーの詳細を確認

## 免責事項

このボットの使用によって生じたいかなる問題についても、開発者は責任を負いません。自己責任でご利用ください。
