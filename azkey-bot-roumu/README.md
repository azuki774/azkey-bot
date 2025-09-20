# azkey-bot-roumu

azkey.azuki.blue 用のルームボット

azkey-bot-roumu は一般的に言う、SNSのログインボーナスbot です。

## インストール

```bash
cd azkey-bot-roumu
pip install -e .
```

## 使用方法

```bash
azkey-bot-roumu status
```

## 開発

```bash
# uv で実行
cd azkey-bot-roumu
uv run python -m azkey_bot_roumu.cli status
```