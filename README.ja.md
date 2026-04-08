# webhook-relay

認証付き Webhook レシーバー。受信したペイロードを GCS に書き出す、インターネット向けの
インジェストゲートウェイ。セキュリティファースト設計。

Google Cloud Run Service として VPC ネットワーク隔離、スケールゼロ課金、
IP 単位のレート制限付きでデプロイ。

## 機能

- **API Key 認証** — 定数時間比較（タイミング攻撃耐性）
- **IP 単位のレート制限** — トークンバケット方式、RPS/バースト設定可
- **リクエストサイズ制限** — デフォルト 25 MB
- **パストラバーサル防止** + ファイル拡張子ホワイトリスト
- **VPC ネットワーク隔離** — Private Google Access 経由の Google API アクセスのみ
- **構造化監査ログ** — JSON 形式で Cloud Logging へ
- **非 root コンテナ** — マルチステージビルド
- **プラグイン可能なバックエンド** — v0.1.0 は GCS、将来的に Pub/Sub, HTTP 等へ拡張可能
- **スケールゼロ** — アイドル時コストゼロ

## 使い方

```bash
# Webhook 経由でファイルアップロード
curl -X POST "https://SERVICE_URL/ingest/gcs/inbox/alert.eml" \
  -H "X-API-Key: YOUR_API_KEY" \
  -H "Content-Type: application/octet-stream" \
  --data-binary @alert.eml

# ヘルスチェック（認証不要）
curl "https://SERVICE_URL/healthz"
```

### API

```
POST /ingest/{backend}/{path...}
  Headers:
    X-API-Key: <secret>         (必須)
    Content-Type: <any>         (パススルー)
  Body: ファイルの生データ
  Response: 201 Created

GET /healthz
  Response: 200 {"status":"ok"}
```

### Power Automate 設定

Power Automate フローで **HTTP** アクションを追加:

| フィールド | 値 |
|-----------|-----|
| メソッド | POST |
| URI | `https://SERVICE_URL/ingest/gcs/inbox/{{triggerOutputs()?['body/subject']}}.eml` |
| ヘッダー | `X-API-Key`: API キー |
| 本文 | メール MIME コンテンツ |

## 設定

| 環境変数 | デフォルト | 説明 |
|----------|-----------|------|
| `WEBHOOK_RELAY_API_KEY` | (必須) | 認証用 API キー |
| `WEBHOOK_RELAY_GCS_BUCKET` | (必須) | 書き込み先 GCS バケット |
| `WEBHOOK_RELAY_GCS_PROJECT` | | GCP プロジェクト ID |
| `WEBHOOK_RELAY_RATE_LIMIT_RPS` | `10` | IP あたりリクエスト/秒 |
| `WEBHOOK_RELAY_RATE_LIMIT_BURST` | `20` | IP あたりバーストサイズ |
| `WEBHOOK_RELAY_MAX_REQUEST_BYTES` | `26214400` | 最大リクエストサイズ (25 MB) |
| `WEBHOOK_RELAY_ALLOWED_EXTENSIONS` | `.eml,.msg` | 許可するファイル拡張子 |
| `PORT` | `8080` | サーバーリッスンポート |

## デプロイ

```bash
cp deploy/deploy.env.template deploy/deploy.env
# deploy/deploy.env を編集

./deploy/deploy.sh deploy/deploy.env

# API キーの生成と保存
openssl rand -hex 32 | \
  gcloud secrets versions add webhook-relay-api-key \
    --data-file=- --project=PROJECT_ID
```

## ビルド

```bash
make build      # ビルド → dist/
make build-all  # クロスコンパイル
make test       # テスト実行
make clean      # dist/ 削除
```

## ドキュメント

- [Security Design](docs/security.md) — 脅威モデル、セキュリティ統制、ネットワーク構成
- [README.md](README.md)（English）
- [CHANGELOG.md](CHANGELOG.md)
