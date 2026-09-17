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
- entertainment、grouped_light の管理
- light service自体の設定（powerupなど）の管理。deviceの名前・archetypeは現在対応
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
│   ├── color/                 # 色変換（xy の表示変換、kelvin ↔ mirek、gamut クリップ）
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
- Delete は bridge 上のリソースを削除する。ただしhue_deviceは管理の解除のみで、機器と設定を残す
- 読み取り専用属性（`id_v1` など）は Computed にし、必要なもの以外は schema に載せない

### `hue_device`（resource）

登録済みdeviceの`metadata.name`と`metadata.archetype`を管理する。

| 属性 | 種別 | 説明 |
|------|------|------|
| `id` | Computed | device UUID |
| `device_id` | Required, RequiresReplace | 登録済みdevice UUID。light service IDではない |
| `light_ids` | Computed, Set of string | 所有するlight service UUID。照明のない機器では空集合 |
| `name` | Optional + Computed | 1〜32文字。省略した場合は実機の値を維持 |
| `archetype` | Optional + Computed | 機器アイコンの種類。省略した場合は実機の値を維持 |

Createは既存deviceを取得して明示されたmetadataのみを更新する。POSTによるペアリングは行わない。
Importはdevice UUIDを受け取り、idとdevice_idを設定する。同じUUIDを複数resourceで管理しない。
Deleteおよびdevice_id変更時の旧binding削除ではBridgeへの書き込みを行わず、機器と設定を残す。
Readで404を受けた場合はstateから除去する。再登録はアプリの担当であり、自動で行わない。
CLIのimport-blocksによるdeviceの一括取り込みは対象外。

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
      color_xy   = { x = 0.5, y = 0.4 }
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

action object:

| 属性 | 種別 | 説明 |
|---|---|---|
| `on` | Optional | 点灯状態 |
| `brightness` | Optional | 0.0〜100.0 |
| `mirek` | Optional, Computed | 色温度（153〜500） |
| `kelvin` | Optional, Computed | 色温度（ケルビン）。`mirek` と排他 |
| `color_xy` | Optional, Computed, object `{x, y}` | CIE xy |

- `mirek` / `kelvin` は ExactlyOneOf ではなく「両方省略可、両方指定は不可」。片方を指定すると他方は provider が計算して埋める
- 色は `color_xy` と独立した `brightness` で指定する。`color_hex` は廃止し、state schema version 1 で旧属性だけを除去して xy を保持する。
- `palette` は Optional + Computed の JSON 文字列。明示時は書き込み、未指定時は実機の値を維持する。actions とは独立して管理し、相互の自動生成は行わない。
- `actions` は Map なので、要素の追加・削除・変更は light ごとに独立した差分として表示される

## 9. 色と色温度の変換

### 方針

- bridge 側の正の値は xy と mirek。HCL の色は xy、色温度は mirek または kelvin で書く
- 比較（差分判定）は必ず xy 空間・mirek 空間で行う。kelvin 同士を比較しない
- 変換は `internal/color` パッケージに純粋関数として実装し、table-driven test で検証する

### 色の表示

- `.tf` は xy と brightness を保持する。表示のために元データを RGB へ置換しない。
- `hue-tf preview` は Terraform plan JSON を読み、ターミナルでは正規化した RGB の色見本を表示する。
- HTML は CSS XYZ の色見本と、明るさを近似的に反映した色見本を並べる。照明の測定輝度とディスプレイの輝度は同一とみなさない。
- `show` の hex は表示用の近似値であり、provider の設定・state 属性ではない。

### kelvin ↔ mirek

- `mirek = round(1_000_000 / kelvin)`、`kelvin = round(1_000_000 / mirek)`
- light の対応範囲（`mirek_min` / `mirek_max`）外の値を指定した場合、bridge が範囲内に丸めて保存する。Read 時の比較では設定値を同じ範囲にクリップしてから比較する（xy の色域クリップと同じ扱い）

### semantic equality

- `color_xy`: 設定値の xy と bridge の値の差が x, y ともに 0.001 以内なら等しいとみなす
- `mirek`: 設定値（kelvin から変換した値、または直接指定）と bridge の値の差が ±1 以内なら等しいとみなす
- 等しい場合、state には設定側の表現（ユーザーが書いた xy / kelvin）をそのまま保持する
- 等しくない場合（drift）、state の xy / mirek を実機の値へ更新する。色温度の kelvin も再計算する

### Read での色域取得

- scene の Read では `actions` に含まれる light の色域・色温度の対応範囲を取得する。照明一覧は provider の Configure ごとに共有し、書き込み後と未知の light ID を参照したときは再取得する。点灯状態やシーン本体はキャッシュしない。
- plan 時には色域を参照しない。クリップは Read 時の比較で吸収する

## 10. 補助 CLI `hue-tf`

`cmd/hue-tf/` に置き、Hue クライアントを provider と共有する。

### v0 で実装するコマンド

| コマンド | 内容 |
|---|---|
| `hue-tf init` | mDNS で bridge を探索し、リンクボタン押下を待って application key を発行する。結果を環境変数の形式で表示する（`export HUE_BRIDGE_HOST=…` / `export HUE_BRIDGE_APPLICATION_KEY=…`） |
| `hue-tf ls <type>` | room / zone / scene / light / device / behavior_instance / behavior_script / button、および switch の対応表を表形式または `--json` で一覧表示する |
| `hue-tf raw <path>` | 任意の v2 エンドポイントに GET し、レスポンスをそのまま出力する |

### v1 以降の候補

- `hue-tf import-blocks`: 未管理リソースの import ブロックを生成する。resource 定義は Terraform の設定生成または手書きに任せる（詳細は第16節）。
- `hue-tf preview [PLAN_JSON] [--html]`: 評価済み設定と差分を色見本として表示
- `hue-tf recall SCENE_UUID [--action ACTION]` と `hue-tf identify DEVICE_UUID_OR_LIGHT_UUID` は実装済み。明示的な実行時操作として Bridge に PUT し、Terraform 定義・state は変更しない。詳細は [実行時操作](runtime-commands.md) を参照。

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
| 6 | 色・色温度の属性 | xy と brightness を独立に指定し、色温度は kelvin / mirek を受ける。比較は xy / mirek 空間 | 色情報の保持と可視化を分離する。色見本は preview が担当し、provider は実機補正との同値判定を行う |
| 7 | TLS | Signify CA を同梱して検証。CN 照合なし。insecure なし | Hue アプリと同等の信頼モデル。bridge ID の同定は host に委ねる |
| 8 | rate limit | provider 内で制御、値は固定、429 リトライあり | 利用者に `-parallelism` を意識させない |
| 9 | Hue クライアント | 自前で薄く書く | provider の都合に合わせた型にする |
| 10 | CLI の置き場・名前 | 同一リポジトリ `cmd/hue-tf` | クライアントの共有と開発速度を優先 |
| 11 | テスト | フェイク bridge で CI | テスト専用の実機がない |
| 12 | provider 設定 | `host` / `application_key`、環境変数 prefix `HUE_BRIDGE_` | |

## 15. 未決定・保留

- `hue-tf import` の HCL ラベル命名規則
- rate limit の固定値の最終調整（実機での測定後）
- light の設定（name / powerup）を管理する `resource "hue_light"` の要否（v1）

## 16. Import ブロック生成

`hue-tf import-blocks` は未管理の room・zone・scene・smart_scene・behavior_instance を発見し、通常の Terraform import ブロックを準備する。

- 引数なしは Bridge 全体の未管理リソースを対象にする。複数 UUID で限定できる。
- state と既存 import ブロックの UUID を照合し、重複生成を防ぐ。
- `--module NAME[.NAME...]` は import 先を指定する。Hue の部屋と module の対応は推測しない。
- import ブロックは root の `imports_hue.tf` にまとめる。既存ブロックは編集・削除しない。
- 既存 resource 定義の更新・削除同期は行わない。比較用 baseline も保持しない。
- resource 定義の生成・編集、state への取り込み、実機への反映は Terraform と利用者の担当とする。
- プレビューは読み取りのみ。`--write` は import ブロックの生成・追記のみ行う。
- 書き込みには復旧情報を保存し、失敗時はファイルを戻す。復旧できない中断では情報を残し、再実行を停止する。

詳細は [Import ブロックの生成](app-to-terraform.md) を参照する。

## 17. スイッチ割り当て

`hue_behavior_instance` は behavior_instance の作成・import・更新・削除に対応する。
name・enabled・configuration と、新規作成時に必要な script_id を定義する。
configuration は jsonencode で表現する JSON オブジェクト全体とし、機種・script ごとの構造を保持する。
script_id の変更は置換。実行状態は読み取り専用。割り当て削除は機器のペアリングを解除しない。
同じ device を参照する割り当てがある場合、新規作成せず既存の import を案内する。
CLI は ls switch による機器との対応表示、import-blocks による未管理分の import ブロック生成に対応する。既存定義の同期・マージは行わない。
運用手順・制約・API 参照は [スイッチ管理](switch-management.md) を参照。


## 18. スマートシーン

`hue_smart_scene` は smart_scene の作成・import・更新・削除に対応する。
曜日ごとの `week_timeslots` は、曜日集合 `recurrence` と順序付きの `timeslots` で定義する。
各枠の `start_time` は `HH:MM:SS` または `sunset`、`scene` は通常シーンの UUID または参照式。
`transition_duration` はミリ秒で既定値60000。group の変更は置換となる。
実行状態と画像は読み取り専用で、作成・更新時に recall や画像変更を送信しない。
import-blocks は未管理分の import ブロック準備に対応する。既存定義の変更・衝突検出は行わない。
詳細は [スマートシーン](smart-scenes.md) を参照する。

現在の設定項目ごとの対応範囲・未対応機能・確認根拠は[設定機能の対応表](feature-coverage.md)を参照。

### シーン画像の管理境界

通常シーン・スマートシーンの画像は管理対象外とする。作成・更新時に `metadata.image` を送信せず、既存画像の再設定や `null` による消去も行わない。
旧stateの `image_id` はschema移行で取り除くが、Bridgeへの操作は行わない。既存の `.tf` に `image_id` がある場合は、その属性を削除する。

### シーンのエフェクトと遷移時間

`actions` の `effects_v2` と `dynamics` はOptional + ComputedのJSON文字列属性とする。
`effects_v2` は `action.effect` と `action.parameters`、`dynamics` はミリ秒単位の `duration` を表現する。
明示指定はその値を管理し、省略時はBridgeから読み取った値を保持して更新リクエストに含める。
属性の削除は解除指示ではない。エフェクト停止には `action.effect = "no_effect"`、即時遷移には `duration = 0` を指定する。
JSON objectであることを検証し、機種固有の内部制約はBridgeで検証する。
