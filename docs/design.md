# terraform-provider-hue 設計仕様書

- リポジトリ: https://github.com/akr4/terraform-provider-hue
- ライセンス: MIT
- ステータス: v0 設計確定（2026-09-08）

## 1. 目的とゴール

Philips Hue の設定（room / zone / scene）を GUI ではなくコードで管理し、Terraform の plan / apply の体験で運用できるようにする。

- v0: 作者自身の環境で room / zone / scene を管理できる
- v1: Terraform Registry / OpenTofu Registry に公開し、Hue の IaC 分野で事実上の標準となる

### 非ゴール（v0）

- light / device のペアリング（bridge への登録）。Hue アプリで行う前提
- automation（behavior_instance）、entertainment、grouped_light の管理
- light 自体の設定（名前、powerup など）の管理
- Hue API v1 のサポート

## 2. 前提となる Hue API の事実

- 対象は CLIP API v2（`https://<bridge>/clip/v2/resource/<type>`）のみ。v1 は使わない
- 認証はヘッダ `hue-application-key`。キーの発行のみ v1 系エンドポイント（`POST /api`、リンクボタン押下が必要）で行う
- bridge は HTTPS のみ。証明書は Signify のプライベート root CA で署名されている。CN は bridge ID で、ホスト名や IP とは一致しない
- リソース ID はすべて UUID
- room の `children` は device、zone の `children` は light service を指す
- scene は `group`（room または zone）に属し、`actions`（light ごとの状態）と `palette`（ダイナミックシーン用）を持つ
- v2 には light の探索・追加（ペアリング）のエンドポイントは存在しない（公開されていない）
- リクエスト頻度に上限があり、超過すると 429 が返る

## 3. 全体構成

```
terraform-provider-hue/
├── main.go                    # provider エントリポイント
├── internal/
│   ├── hue/                   # Hue v2 クライアント（自前・薄い）
│   │   ├── client.go          # HTTP、TLS、rate limit、リトライ
│   │   ├── types.go           # room / zone / scene / light / device の struct
│   │   └── ca.pem             # Signify root CA（埋め込み）
│   ├── color/                 # 色変換（hex ↔ xy、kelvin ↔ mirek、gamut クリップ）
│   ├── provider/              # plugin-framework の provider / resources / data sources
│   └── fakebridge/            # テスト用フェイク bridge（httptest）
├── cmd/hue-tf/                # 補助 CLI
├── docs/                      # tfplugindocs 生成物 + この仕様書
└── examples/
```

- Terraform Plugin Framework（protocol v6）を使う
- Hue クライアントは provider と CLI で共有する
- 言語は Go

## 4. Provider 設定

```hcl
provider "hue" {
  host            = "192.168.1.10"   # HUE_BRIDGE_HOST
  application_key = "..."            # HUE_BRIDGE_APPLICATION_KEY
}
```

| 属性 | 型 | 必須 | 環境変数 | 説明 |
|---|---|---|---|---|
| `host` | string | yes（環境変数で代替可） | `HUE_BRIDGE_HOST` | bridge の IP アドレスまたはホスト名。スキームやポートは含めない |
| `application_key` | string (sensitive) | yes（環境変数で代替可） | `HUE_BRIDGE_APPLICATION_KEY` | `hue-application-key` の値 |

- bridge ID の設定項目は持たない
- `insecure` などの TLS 検証を無効化する設定は持たない
- rate limit を変更する設定は持たない

## 5. TLS

- 同梱した Signify root CA でサーバー証明書のチェーンを検証する
- 証明書の CN / SAN とホスト名の照合は行わない（`VerifyPeerCertificate` でチェーン検証のみ実装する）
- 検証を無効化する手段は提供しない
- 保証する内容は「Signify の CA で署名された本物の Hue bridge と通信していること」。どの bridge かの同定は `host` の指定に委ねる

## 6. Rate limit とリトライ

- クライアント内にトークンバケット + セマフォを持ち、bridge へのリクエストを制限する
- 値は固定（設定不可）。初期値は同時接続 1、秒間 5 リクエストとし、実機での挙動を見て調整する
- 429 を受けたら `Retry-After` に従って待って再試行する。上限回数を超えたらエラー
- Terraform の `-parallelism` に依存しない

## 7. Data sources

### `hue_light`

```hcl
data "hue_light" "desk" {
  id = "3f1c…"
}
```

| 属性 | 種別 | 説明 |
|---|---|---|
| `id` | Required | light の UUID |
| `name` | Computed | `metadata.name` |
| `device_id` | Computed | 所有 device の UUID（room の children に渡す用） |
| `gamut_type` | Computed | `A` / `B` / `C` / `other`。色変換の色域に使う |
| `supports_color` | Computed | `color` を持つか |
| `supports_color_temperature` | Computed | `color_temperature` を持つか |
| `mirek_min` / `mirek_max` | Computed | 対応する色温度の範囲 |

### `hue_device`

```hcl
data "hue_device" "ceiling" {
  id = "…"
}
```

| 属性 | 種別 | 説明 |
|---|---|---|
| `id` | Required | device の UUID |
| `name` | Computed | `metadata.name` |
| `model_id` | Computed | `product_data.model_id` |
| `light_ids` | Computed | この device が持つ light service の UUID 一覧 |

- キーは `id` のみ。`name` による検索は提供しない
- 対象が見つからない場合はエラー

## 8. Resources

共通事項:

- ID は bridge の UUID をそのまま使う
- `import` に対応する（`terraform import hue_room.x <uuid>` および `import` ブロック）
- Delete は bridge 上のリソースを削除する
- 読み取り専用属性（`id_v1` など）は Computed にし、必要なもの以外は schema に載せない

### `hue_room`

```hcl
resource "hue_room" "study" {
  name      = "Study"
  archetype = "office"
  children  = [data.hue_light.desk.device_id]
}
```

| 属性 | 種別 | 説明 |
|---|---|---|
| `id` | Computed | UUID |
| `name` | Required | `metadata.name` |
| `archetype` | Optional | `metadata.archetype`。未指定なら `other` |
| `children` | Required, Set of string | device の UUID の集合 |

- `children` は Set。順序の差は差分にならない
- ForceNew の属性はない

### `hue_zone`

`hue_room` と同じ構造。`children` は light の UUID の集合（device ではない）。

### `hue_scene`

```hcl
resource "hue_scene" "evening" {
  name  = "Evening"
  group = hue_room.study.id

  actions = {
    (data.hue_light.desk.id) = {
      on          = true
      brightness  = 40
      kelvin      = 2700
    }
    (data.hue_light.floor.id) = {
      on         = true
      brightness = 20
      color_hex  = "#ff8800"
    }
  }

  speed        = 0.6
  auto_dynamic = false
}
```

| 属性 | 種別 | 説明 |
|---|---|---|
| `id` | Computed | UUID |
| `name` | Required | `metadata.name` |
| `group` | Required, **RequiresReplace** | 所属する room または zone の UUID。変更時は再作成 |
| `actions` | Required, Map of object | キーは light の UUID。値は下記 action object |
| `speed` | Optional, Computed | 0.0〜1.0 |
| `auto_dynamic` | Optional, Computed | デフォルト false |
| `image_id` | Optional, Computed | `metadata.image.rid`。import 時に保持する。新規作成では省略可 |

action object:

| 属性 | 種別 | 説明 |
|---|---|---|
| `on` | Optional | 点灯状態 |
| `brightness` | Optional | 0.0〜100.0 |
| `mirek` | Optional, Computed | 色温度（153〜500） |
| `kelvin` | Optional, Computed | 色温度（ケルビン）。`mirek` と排他 |
| `color_xy` | Optional, Computed, object `{x, y}` | CIE xy |
| `color_hex` | Optional, Computed | `#rrggbb`。`color_xy` と排他 |

- `mirek` / `kelvin` は ExactlyOneOf ではなく「両方省略可、両方指定は不可」。片方を指定すると他方は provider が計算して埋める
- `color_xy` / `color_hex` も同様
- `palette` は v0 では読み取り専用（Computed）とし、書き込みは v1 以降で対応する
- `actions` は Map なので、要素の追加・削除・変更は light ごとに独立した差分として表示される

## 9. 色と色温度の変換

### 方針

- bridge 側の正の値は xy と mirek。HCL では hex と kelvin でも書ける
- 比較（差分判定）は必ず xy 空間・mirek 空間で行う。hex 同士、kelvin 同士を比較しない
- 変換は `internal/color` パッケージに純粋関数として実装し、table-driven test で検証する

### hex → xy

1. sRGB をガンマ補正して線形 RGB に変換
2. Wide RGB D65 の変換行列で XYZ に変換（Philips の公開している式に従う）
3. xy に正規化
4. 対象 light の `gamut_type`（Read 時に取得）に応じて、色域三角形の外側なら最も近い辺上の点にクリップ
5. 小数 4 桁に丸める

### xy → hex（表示用）

- 輝度は最大（Y = 1 相当）として逆変換し、sRGB にクリップして `#rrggbb` にする
- 近似値であり、往復で元の hex に戻ることは保証しない

### kelvin ↔ mirek

- `mirek = round(1_000_000 / kelvin)`、`kelvin = round(1_000_000 / mirek)`
- light の対応範囲（`mirek_min` / `mirek_max`）外の値を指定した場合、bridge が範囲内に丸めて保存する。Read 時の比較では設定値を同じ範囲にクリップしてから比較する（xy の色域クリップと同じ扱い）

### semantic equality

- `color_xy`: 設定値（hex から変換した値、または直接指定した xy）と bridge の値の差が x, y ともに 0.001 以内なら等しいとみなす
- `mirek`: 設定値（kelvin から変換した値、または直接指定）と bridge の値の差が ±1 以内なら等しいとみなす
- 等しい場合、state には設定側の表現（ユーザーが書いた hex / kelvin）をそのまま保持する
- 等しくない場合（drift）、state には bridge の値と、そこから計算した表示用の hex / kelvin を入れる。plan には両方の行が差分として表示される

### Read での色域取得

- scene の Read では `actions` に含まれる各 light の `gamut_type` を取得する（1 回の `GET /resource/light` で全件取得しキャッシュする）
- plan 時には色域を参照しない。クリップは Read 時の比較で吸収する

## 10. 補助 CLI `hue-tf`

`cmd/hue-tf/` に置き、Hue クライアントを provider と共有する。

### v0 で実装するコマンド

| コマンド | 内容 |
|---|---|
| `hue-tf init` | mDNS で bridge を探索し、リンクボタン押下を待って application key を発行する。結果を環境変数の形式で表示する（`export HUE_BRIDGE_HOST=…` / `export HUE_BRIDGE_APPLICATION_KEY=…`） |
| `hue-tf ls <type>` | room / zone / scene / light / device を表形式または `--json` で一覧表示する |
| `hue-tf raw <path>` | 任意の v2 エンドポイントに GET し、レスポンスをそのまま出力する |

### v1 以降の候補

- `hue-tf import`: bridge 上の room / zone / scene を resource ブロック + `import` ブロックとして、light / device を data ブロックとして HCL 生成する。ラベルの命名規則（日本語名の扱い）は別途決定
- `hue-tf color <hex> --gamut <type>`: 色変換の確認
- `hue-tf recall <scene>`、`hue-tf identify <light>`

### 認証情報の受け渡し

- CLI も `HUE_BRIDGE_HOST` / `HUE_BRIDGE_APPLICATION_KEY` を読む。provider と同じ

## 11. テスト

- unit test: 色変換、semantic equality、クライアントの rate limit / リトライは純粋関数または httptest で検証する
- acceptance test: `internal/fakebridge` に v2 の `/clip/v2/resource/*` を模したフェイクサーバーを実装し、`TF_ACC=1` のテストを CI（GitHub Actions）でフェイクに対して実行する
- フェイクのレスポンスは、実機から取得した JSON を fixture として持ち、可能な限り実機の挙動（xy の丸め、404 / 429 の形式）に合わせる
- テスト専用の実機 bridge は持たない。作者の自宅 bridge に対する確認は手動で行い、自動テストの対象にしない

## 12. リリースと公開

- GoReleaser + GPG 署名。terraform-provider-scaffolding-framework のワークフローを踏襲する
- ドキュメントは `tfplugindocs` で schema から生成する
- 公開先は Terraform Registry（namespace `akr4`）と OpenTofu Registry
- CLI のバイナリは provider のリリース成果物とは分けて配布する（GoReleaser の設定を分離する）
- README の冒頭に「light / device の追加は Hue アプリで行う。provider は bridge に登録済みのものを管理する」と明記する

## 13. 実装順序（v0）

1. Hue クライアント（TLS、rate limit、GET / POST / PUT / DELETE、型定義）と `hue-tf raw` / `ls`
2. `hue-tf init`
3. provider の骨組みと `data "hue_light"` / `data "hue_device"`
4. `resource "hue_room"`（CRUD + import）
5. `resource "hue_zone"`
6. `internal/color` と semantic equality
7. `resource "hue_scene"`（CRUD + import）
8. フェイク bridge と acceptance test
9. 作者環境の既存構成を import して `terraform plan` が No changes になることを確認

## 14. 決定記録

| # | 項目 | 決定 | 理由・備考 |
|---|---|---|---|
| 1 | リポジトリ名義 | 個人 `akr4` | Registry namespace は公開後に変更不可。個人 OSS として出す |
| 2 | ライセンス | MIT | 利用・フォークの障壁を最小にする |
| 3 | 補助 CLI | 作る。`init` を最初から載せる | key 発行は対話が要るため provider に入れない。CLI に集約する |
| 4 | children / actions の型 | children は Set、actions は light ID キーの Map | 順序ノイズを排除しつつ、light 単位の差分を読みやすくする |
| 5 | data のキー | `id` のみ | name は重複しうる。生成 CLI が id を埋める前提 |
| 6 | 色・色温度の属性 | hex / xy、kelvin / mirek を両方受ける内部変換。比較は xy / mirek 空間 | 人間が書ける・読める・drift も読める。semantic equality で往復のずれを吸収する |
| 7 | TLS | Signify CA を同梱して検証。CN 照合なし。insecure なし | Hue アプリと同等の信頼モデル。bridge ID の同定は host に委ねる |
| 8 | rate limit | provider 内で制御、値は固定、429 リトライあり | 利用者に `-parallelism` を意識させない |
| 9 | Hue クライアント | 自前で薄く書く | provider の都合に合わせた型にする |
| 10 | CLI の置き場・名前 | 同一リポジトリ `cmd/hue-tf` | クライアントの共有と開発速度を優先 |
| 11 | テスト | フェイク bridge で CI | テスト専用の実機がない |
| 12 | provider 設定 | `host` / `application_key`、環境変数 prefix `HUE_BRIDGE_` | |

## 15. 未決定・保留

- `hue-tf import` の HCL ラベル命名規則
- scene の `palette` の書き込み対応（v1）
- rate limit の固定値の最終調整（実機での測定後）
- light の設定（name / powerup）を管理する `resource "hue_light"` の要否（v1）

## 16. アプリから Terraform への取り込み

`hue-tf pull` は実機からローカルの HCL 定義に変更候補を取り込む。Bridge や state へは書き込まない。

- `pull hue_room.NAME` / `pull hue_zone.NAME`: name、archetype、children のリテラルを更新する。
  children は所属集合として比較する。
- `pull hue_scene.NAME`: on、brightness、mirek/kelvin、color_xy と色温度↔カラーの切り替えを取り込む。
- `pull --new`: state に未登録の room・zone・scene を列挙する。
- `pull --new UUID hue_TYPE.NAME`: 新規定義と `terraform import` コマンドを提示する。
- デフォルトはプレビュー。`--write` 指定時のみ `.tf` を更新・生成する。既存ファイルの更新はバックアップを作成する。
- 既存リソースは state の UUID で対応付ける。実機・state・HCL の値を比較し、ローカル編集との競合は自動解決しない。
- root とその配下の単独使用ローカル module の直接定義を対象にする。完全な module アドレスで対応付ける。
  変数・計算式、共有/外部 source、count/for_each、provider alias の編集には対応しない。
- 新規定義の生成と state への登録は別操作。ユーザーが import した後に plan で差分を確認する。

詳細な対応範囲、コメント保持の制約、state の同期手順は [アプリからの取り込み手順](app-to-terraform.md) を参照する。
