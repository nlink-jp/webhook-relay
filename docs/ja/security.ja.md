# Security Design — webhook-relay

この文書は webhook-relay に実装されたセキュリティ構成、脅威モデル、セキュリティ統制を述べる。
セキュリティレビューと本番配備の承認のためのものである。

---

## 目次

1. [システム概要](#システム概要)
2. [脅威モデル](#脅威モデル)
3. [セキュリティ統制](#セキュリティ統制)
4. [ネットワーク構成](#ネットワーク構成)
5. [認証と認可](#認証と認可)
6. [入力の検証](#入力の検証)
7. [レート制限](#レート制限)
8. [監査ログ](#監査ログ)
9. [資格情報の管理](#資格情報の管理)
10. [コンテナのセキュリティ](#コンテナのセキュリティ)
11. [IAM の最小権限の原則](#iam-の最小権限の原則)
12. [残存リスク](#残存リスク)

---

## システム概要

webhook-relay は、外部のソース（例: Power Automate）から webhook のペイロードを受け取り、
GCS バケットに書き込むステートレスな HTTP サービスである。VPC によるネットワーク分離を伴う
Google Cloud Run Service として配備される。

**データの流れ:**
```
外部の送信元 (Power Automate)
    │
    │ HTTPS POST /ingest/gcs/{path}
    │ Header: X-API-Key
    │ Body: メールファイル (バイナリ)
    │
    ▼
┌────────────────────────────────────┐
│ Cloud Run Service                  │
│ (webhook-relay)                    │
│                                    │
│ インターネット ingress (HTTPS/TLS) │
│     ↓                              │
│ ミドルウェアの連鎖:                │
│   1. 監査ログ                      │
│   2. セキュリティヘッダー          │
│   3. リクエストサイズの上限        │
│   4. IP ごとのレート制限           │
│   5. API キー認証                  │
│   6. HTTP メソッドの強制           │
│     ↓                              │
│ パスの検証                         │
│     ↓                              │
│ GCS への書き込み (VPC egress 経由) │
│                                    │
│ VPC Direct Egress ─────────────────┼──┐
└────────────────────────────────────┘  │
                                        │
┌───────────────────────────────────────┴──┐
│ VPC: webhook-relay-vpc                   │
│ ┌──────────────────────────────────────┐ │
│ │ Subnet: 10.100.0.0/28                │ │
│ │ Private Google Access: ON            │ │
│ │                                      │ │
│ │ Egress → Google API のみ             │ │
│ │ (インターネット egress なし)         │ │
│ └──────────────────────────────────────┘ │
│                                          │
│ Firewall: deny-all-ingress               │
└──────────────────────────────────────────┘
```

**このサービスが行わないこと:**
- データをローカルに保存すること（ステートレス）
- GCS から読むこと（書き込み専用）
- 受け取ったファイルを実行または解釈すること
- セッションや cookie を保持すること
- 管理インターフェースを公開すること

---

## 脅威モデル

### 資産

| Asset | Sensitivity | Location |
|-------|-------------|----------|
| API キー | High | GCP Secret Manager → 環境変数として注入 |
| メールファイル（通信中） | Medium | TLS で暗号化された HTTPS のボディ |
| メールファイル（保存時） | Medium | GCS バケット（サーバーサイド暗号化） |
| 監査ログ | Medium | Cloud Logging（構造化 JSON） |

### 脅威アクター

| Actor | Capability | Motivation |
|-------|-----------|------------|
| 外部の攻撃者 | インターネットへのアクセス、自動スキャン | データの持ち出し、DoS、横展開 |
| 侵害された Power Automate アカウント | 妥当な API キー、リクエストを送る能力 | 不正なデータの注入 |
| 内部者（クラウド管理者） | GCP プロジェクトへのアクセス | 規定違反 |

### 脅威と緩和策

| ID | Threat | Impact | Control | Residual Risk |
|----|--------|--------|---------|---------------|
| T1 | API キーの総当たり | 不正アクセス | 定時間比較、レート制限、256 ビットの鍵 | Low |
| T2 | 大きなペイロードによる DoS | サービスの停止 | 25 MB のリクエストサイズ上限、Cloud Run のオートスケール上限 | Low |
| T3 | リクエストの洪水による DoS | サービスの停止、費用 | IP ごとのレート制限（10 rps）、最大インスタンス数の上限 | Low |
| T4 | パストラバーサル | 意図しない場所への書き込み | パスの検証（`..` を許さない、拡張子のホワイトリスト） | Low |
| T5 | egress 経由のデータ持ち出し | 機密データの漏えい | インターネットへの egress が無い VPC、Private Google Access のみ | Low |
| T6 | API キーの漏えい | 不正アクセス | Secret Manager、鍵の値を記録しない、鍵のローテーション | Medium |
| T7 | 悪性ファイルのアップロード | 下流システムの侵害 | 拡張子のホワイトリスト、サイズ上限。ファイルは実行されない。 | Medium（下流に依存） |
| T8 | TLS のダウングレード | 盗聴 | Cloud Run が HTTPS を強制、HSTS | Low |
| T9 | ログインジェクション | ログの改ざん | 構造化 JSON ログ、ログのキーにユーザー入力を入れない | Low |

---

## セキュリティ統制

### 統制の一覧

| Category | Control | Implementation |
|----------|---------|----------------|
| 認証 | API キー（X-API-Key ヘッダー） | `internal/auth/apikey.go` |
| 認可 | バックエンド名の検証（英数字のみ） | `internal/server/server.go` |
| 入力の検証 | パストラバーサルの防止 | `internal/middleware/security.go` |
| 入力の検証 | ヌルバイトの拒否 | `internal/middleware/security.go` |
| 入力の検証 | 制御文字／バックスラッシュの拒否 | `internal/middleware/security.go` |
| 入力の検証 | ファイル拡張子のホワイトリスト | `internal/middleware/security.go` |
| 入力の検証 | リクエストサイズの上限 | `internal/middleware/security.go` |
| インジェクション対策 | `json.Marshal` による JSON 応答（文字列連結を使わない） | `internal/server/server.go` |
| インジェクション対策 | エラーメッセージは静的（ユーザー入力を反射しない） | `internal/server/server.go` |
| CSRF 対策 | cookie／セッションを使わず、API キー認証のみ | 設計による |
| SSRF 対策 | ユーザーが制御する外向き URL が無く、VPC の egress を制限 | 設計 + `deploy/deploy.sh` |
| ヘッダーインジェクション | Go の `net/http` がヘッダーの CR/LF を除去し、入力では制御文字を拒否 | ランタイム + `internal/middleware/security.go` |
| レート制限 | IP ごとのトークンバケット | `internal/middleware/ratelimit.go` |
| 監査 | 構造化 JSON のリクエストログ | `internal/middleware/logging.go` |
| ネットワーク | VPC による分離 + Private Google Access | `deploy/deploy.sh` |
| ネットワーク | ingress を全拒否するファイアウォール | `deploy/deploy.sh` |
| トランスポート | TLS 1.2+（Cloud Run が強制） | Cloud Run プラットフォーム |
| コンテナ | 非 root ユーザー、最小のベースイメージ | `deploy/Dockerfile` |
| シークレット | GCP Secret Manager | `deploy/deploy.sh` |
| IAM | 最小権限（GCS へ書き込み専用） | `deploy/deploy.sh` |

---

## ネットワーク構成

### VPC の設計

```
┌─────────────────────────────────────────────┐
│ webhook-relay-vpc                           │
│                                             │
│  Subnet: webhook-relay-subnet               │
│  CIDR:   10.100.0.0/28 (16 IP アドレス)     │
│  Private Google Access: ENABLED             │
│                                             │
│  ファイアウォール規則:                      │
│    webhook-relay-vpc-deny-all-ingress       │
│    Direction: INGRESS                       │
│    Action: DENY ALL                         │
│    Source: 0.0.0.0/0                        │
│    Priority: 65534                          │
│                                             │
│  Egress: Google API のみ                    │
│    (Private Google Access 経由)             │
│    インターネットへの egress は不可能       │
└─────────────────────────────────────────────┘
```

### なぜ VPC で分離するのか？

1. **インターネットへの egress が無い:** アプリケーションが侵害されたとしても、任意の
   インターネットホストへ外向きの接続を行うことはできない。到達できる先は Private Google
   Access 経由の Google API（GCS、Secret Manager、Cloud Logging）だけである。

2. **ingress を全拒否するファイアウォール:** VPC のファイアウォールがサブネットへのすべての
   ingress トラフィックを遮断する。Cloud Run は VPC のファイアウォールとは独立に自身の
   ingress を扱うので、webhook のエンドポイントは到達可能なままだが、VPC 内の他のリソースには
   アクセスできない。

3. **最小のサブネット:** /28 の CIDR（16 個の IP）は影響範囲を最小にする。このネットワークを
   共有する他のサービスは無い。

### Cloud Run の Ingress

Cloud Run の ingress は `all` に設定している。サービスは Power Automate（予測できない
Microsoft 所有の IP レンジ）からのトラフィックを受け取らなければならないからである。認証は
ネットワーク層ではなくアプリケーション層（API キー）で扱う。

---

## 認証と認可

### API キー認証

- **仕組み:** `X-API-Key` ヘッダーを保存されたシークレットと照合する
- **比較:** `crypto/subtle.ConstantTimeCompare`（タイミング安全）
- **鍵の保管:** GCP Secret Manager、環境変数として注入
- **鍵の生成:** `openssl rand -hex 32`（256 ビットのエントロピー）
- **鍵のローテーション:** 新しいシークレットのバージョンを作り、Cloud Run を更新し、古い
  バージョンを失効させる

### なぜ API キーなのか（OAuth2 / JWT ではなく）？

Power Automate の HTTP コネクタはカスタムヘッダーには対応しているが、OAuth2 の能力は限られて
いる（とくに Entra ID のアプリ登録が無い場合）。カスタムヘッダーに入れた API キーが、最も
互換性の高い認証の仕組みである。

### 定時間比較

通常の文字列比較（`==`）はタイミングの差から情報を漏らす: 攻撃者はレスポンスの遅延を測ることで
鍵の何バイトが正しいかを判定できる。`crypto/subtle.ConstantTimeCompare` はどのバイトが異なるか
に関わらず一定の時間で完了し、タイミングのサイドチャネル攻撃を防ぐ。

---

## 入力の検証

### パスの検証

すべてのオブジェクトパスは、どのバックエンドに届く前にも検証される:

| Check | Rationale |
|-------|-----------|
| 空でないこと | バケットのルートへの書き込みを防ぐ |
| `..` や `.` のセグメントが無いこと | ディレクトリトラバーサルを防ぐ |
| `path.Clean()` との一致 | エンコードされたトラバーサル（`//`、末尾の `/`）を検出する |
| 拡張子のホワイトリスト | 既定では `.eml` と `.msg` のみ |

### リクエストサイズの上限

- 既定: 25 MB
- `http.MaxBytesReader` によって強制し、超えた場合は 413 を返す
- メモリの枯渇とストレージの悪用を防ぐ

### 途中で終わったアップロードは確定しない

GCS への書き込みはリクエストボディを `storage.Writer` にストリームする。読み取りが途中で
失敗したら（上限超過、読み取りタイムアウト、クライアントの切断）、ライターのコンテキストを
キャンセルしてアップロードを中断する。オブジェクトが確定するのはボディ全体を読み切ったときだけ
である。

これが重要なのは、固定している storage ライブラリ（v1.62.0）について、ソースから確かめ
`internal/backend/gcs/gcs_test.go` で偽のアップロード先に対して再現した 2 つの事実があるから
である: 部分的に書いたあとの `Writer.Close` はそれまでのバイト列を切り詰められたオブジェクトとして
確定し、1 バイトも受け取っていないライターの `Close` は空のオブジェクトを作る。以前のコードは
エラー経路で `Close` を呼んでいたため、上限超過や中断されたアップロードは送信元が受け取る 500 の
裏で切り詰められたオブジェクトを残していた。再送すると、その隣に完全なオブジェクトができる。

### HTTP メソッドの強制

- `/ingest/` のルートでは `POST` のみを受け付ける
- それ以外のメソッドはすべて 405 Method Not Allowed を返す
- `GET /healthz` が唯一の POST 以外のエンドポイントである（認証不要、`{"status":"ok"}` を返す）

---

## レート制限

### アルゴリズム

`golang.org/x/time/rate` を用いた IP ごとのトークンバケットによるレート制限:

| Parameter | Default | Configurable |
|-----------|---------|:---:|
| Rate | 10 リクエスト/秒 | Yes（`WEBHOOK_RELAY_RATE_LIMIT_RPS`） |
| Burst | 20 リクエスト | Yes（`WEBHOOK_RELAY_RATE_LIMIT_BURST`） |
| Response | 429 + `Retry-After: 1` | — |

### IP の抽出

Cloud Run は観測したクライアント IP を `X-Forwarded-For` の**右端**に付け足し、クライアントが
送ってきた値はそのまま通す。したがって右端以外のすべての要素は攻撃者が選べる値である。

1. `X-Forwarded-For` の右端の要素を採る。ただし IP アドレスとして解釈できる場合に限る。
2. そうでなければ `RemoteAddr` にフォールバックする。
3. IPv6 のクライアントは /64 プレフィックスで束ねる。1 つの契約者は通常それ以上の
   アドレス空間を持つからである。

リクエストごとに左端の要素を変えるクライアントも、同じ 1 つのバケットを共有する。
クライアントが送る値で任意のキーを作ることはできない。

### 状態の上限

- 訪問者のエントリは 3 分間活動が無いと退去させる。
- 訪問者マップは最大 10,000 キーまでしか持たない。満杯のときは最も長く見ていないエントリを
  退去させて新しいエントリの場所を作る。退去させられたクライアントは次のリクエストで新しい
  バケットから始まるだけである。どれだけ多くのクライアントが現れてもメモリは数 MB に収まる。
- `http.Server.MaxHeaderBytes` は 64 KiB（Go の既定は 1 MiB）とし、すべてのルートで
  リクエストごとのヘッダーのメモリを抑える。

---

## 監査ログ

すべてのリクエストは構造化 JSON として stdout に記録され、Cloud Run がそれを Cloud Logging へ
転送する:

```json
{
  "timestamp": "2026-04-09T10:00:00Z",
  "method": "POST",
  "path": "/ingest/gcs/inbox/alert.eml",
  "remote_addr": "10.0.0.1:1234",
  "x_forwarded_for": "203.0.113.5",
  "status": 201,
  "duration_ms": 45,
  "bytes_in": 102400,
  "user_agent": "PowerAutomate/1.0"
}
```

### 記録しないもの

- API キーの値（妥当なものも不正なものも）
- リクエストボディの内容
- レスポンスボディの内容
- 内部のエラーの詳細（一律 "backend write failed" として返す）

---

## 資格情報の管理

| Credential | Storage | Access Method | Rotation |
|-----------|---------|---------------|----------|
| API キー | Secret Manager | Cloud Run のシークレットマウント（環境変数） | 新しいバージョンを追加 → 再配備 → 古いバージョンを削除 |
| GCS へのアクセス | サービスアカウント（ADC） | IAM バインディング（鍵ファイル無し） | GCP が管理（手動のローテーション無し） |

### 鍵ファイルを使わない

このサービスは Cloud Run 上で Application Default Credentials（ADC）を使い、それは紐づけられた
サービスアカウントに解決される。JSON の鍵ファイルは作成も保管も配備もされない。

---

## コンテナのセキュリティ

| Measure | Implementation |
|---------|----------------|
| マルチステージビルド | `golang:1.24-alpine` でビルドし、`alpine:3.21` で実行 |
| 非 root ユーザー | `adduser -D -H appuser` + `USER appuser` |
| 最小のイメージ | Alpine ベース、`ca-certificates` のみを導入 |
| 静的バイナリ | `CGO_ENABLED=0`、動的リンク無し |
| シェルが不要 | エントリポイントはバイナリそのもの |

---

## IAM の最小権限の原則

サービスアカウントは必要最小限の権限しか持たず、それぞれ存在する最も狭いスコープに束縛する:

| Role | Scope | Justification |
|------|-------|---------------|
| `roles/storage.objectCreator` | 受信バケット（`gs://${GCS_BUCKET}`）のみ | GCS へオブジェクトを書き込む。`objectAdmin` では**ない** — 既存のオブジェクトを読むこと・一覧すること・削除することはできない。プロジェクト内の他のバケットには書けない。 |
| `roles/secretmanager.secretAccessor` | シークレット `webhook-relay-api-key` のみ | API キーを読む。プロジェクト内の他のシークレットは読めない。 |
| `roles/logging.logWriter` | プロジェクト | Cloud Logging へ構造化ログを書き込む。ログにはこれより狭い束縛先が無い。 |

したがってサービスの識別情報が侵害されても、できることは 1 つのバケットへの追記と 1 つの
シークレットの読み取りに限られ、プロジェクトに他に何があっても変わらない。以前の `deploy.sh`
は前 2 つのロールをプロジェクト全体に束縛しており、その配備では手で外すまで束縛が残る。
コマンドは README.ja.md「既存デプロイの更新」にある。

**とくに除外しているもの:**
- `storage.objectViewer` は無い — サービスは既存のオブジェクトを読めない
- `storage.objectAdmin` は無い — サービスはオブジェクトを削除できない
- `run.invoker` は無い — 必要がない（サービスは自身で認証を扱う）
- `iam.serviceAccountUser` は無い — 他のアカウントに成り代わることはできない

---

## 残存リスク

| Risk | Severity | Mitigation Status | Notes |
|------|----------|-------------------|-------|
| Power Automate の管理者経由での API キーの侵害 | Medium | Accepted | 鍵は直ちにローテーションできる。監査ログによって検知できる。 |
| 悪性のファイル内容（例: エクスプロイトを仕込んだ .eml） | Medium | Partial | webhook-relay はファイルをパースも実行もしない。下流の利用者（mail-triage）が検証しなければならない。 |
| Cloud Run のコールドスタートの遅延（1-2s） | Low | Accepted | webhook の呼び出し側（Power Automate）はこの遅延を許容する。 |
| GCS バケットの設定ミス（公開アクセス） | Medium | Mitigated | 均一なバケットレベルのアクセスを強制している。既定で公開アクセスは無い。 |
| 分散した IP によるレート制限の回避 | Low | Accepted | Cloud Run の最大インスタンス数の上限が全体の費用を抑える。IP ごとのレート制限は単一の発信元による悪用に対処する。 |
