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
- bridge は HTTPS のみ。証明書は Signify のプライベート root CA で署名されている。CN は bridge ID（実機では大文字の16桁の16進数）で、ホスト名や IP とは一致しない
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
  host            = "192.168.1.10"     # HUE_BRIDGE_HOST
  bridge_id       = "001788fffe000000" # HUE_BRIDGE_ID
  application_key = "..."              # HUE_BRIDGE_APPLICATION_KEY
}
```

| 属性 | 型 | 必須 | 環境変数 | 説明 |
|---|---|---|---|---|
| `host` | string | yes（環境変数で代替可） | `HUE_BRIDGE_HOST` | bridge の IP アドレスまたはホスト名。スキームやポートは含めない |
| `bridge_id` | string | yes（環境変数で代替可） | `HUE_BRIDGE_ID` | bridge ID（16桁の16進数）。証明書の CN と照合する |
| `application_key` | string (sensitive) | yes（環境変数で代替可） | `HUE_BRIDGE_APPLICATION_KEY` | `hue-application-key` の値 |

- `insecure` などの TLS 検証を無効化する設定は持たない
- rate limit を変更する設定は持たない

## 5. TLS

- 同梱した Signify root CA でサーバー証明書のチェーンを検証する
- 証明書の CN を `bridge_id` と大文字小文字を区別せずに照合する。チェーン検証だけでは、同じ LAN にある別のブリッジ（本物の Signify 署名付き）を区別できないため
- ホスト名や IP との照合はしない。CN が bridge ID なので、Go 標準の検証は使えない（`VerifyPeerCertificate` で自前に検証する）
- `hue-tf init` で ID が分からないとき（`HUE_BRIDGE_HOST` だけを設定した場合）は、チェーン検証を通った証明書の CN を ID として採用し、以後はそれと照合する（初回接続時の信頼）
- 検証を無効化する手段は提供しない
- 保証する内容は「Signify の CA で署名された本物の Hue bridge と通信していること」。どの bridge かの同定は `host` の指定に委ねる

## 6. Rate limit とリトライ

- クライアント内にトークンバケット + セマフォを持ち、bridge へのリクエストを制限する
- GETは最大4並列。送信開始は秒間5件から始め、成功10件ごとに1件増やし、上限は秒間10件とする（クライアント単位、設定不可）
- GETの429では送信ペースを半減（下限は秒間1件）し、Retry-Afterまたは指数バックオフを待機中の全GETで共有する。既に送信済みのリクエストは取り消さない
- POST / PUT / DELETEは別の制御で直列・秒間5件を維持する。読取と書込のレートは独立し、合計上限ではない
- 429より前に送ったGETの遅れて届いた成功応答では、減速後のペースを引き上げない
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
- Delete は bridge 上のリソースを削除する。ただしhue_deviceは管理の解除のみで、機器と設定を残す。hue_room / hue_zone は、中に scene が残っていると削除しない（`force_destroy`）
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
| `force_destroy` | Optional | 既定 `false`。`true` のときだけ、scene が残っていても削除する。Terraform 側だけの値 |

- `children` は Set。順序の差は差分にならない
- ForceNew の属性はない

実機での確認（2026-10-15、write-check キット）:

- room や zone を削除すると、bridge はそこに属する scene も一緒に削除する。Hue アプリで作った scene も含む。plan に出るのは room / zone の削除だけで、apply もエラーにならない
- ほかの room に属する device を `children` に入れて room を作ると、bridge は device を移す。移動元の room の scene からはその light の action が消え、`last_actions_update.source` が `autogrow` になる
- room を削除すると、その device はどの room にも属さなくなり、`bridge_home` の `children` に入る
- smart_scene も bridge が削除する。ただし少し遅れる。Hue アプリを閉じた状態で、削除の約1秒後には残っていて、約2秒後には消えていた

削除時の保護:

- `force_destroy` が `false` の間、Delete は scene と smart_scene の一覧を取得する。`group` がこの room / zone を指すものが残っていれば、名前を並べたエラーを返して削除しない。S3 バケットの `force_destroy` と同じ考え方
- Terraform で管理する scene は、`group` でこの room / zone の `id` を参照していれば依存関係で先に削除される。判定の時点で残っているのは管理外の scene になる。UUID を直接書いた scene は削除順が保証されないため、エラーになることがある。その場合はもう一度 apply すればよい
- Delete は state の値を使う。`force_destroy = true` は削除の前に apply しておく
- `force_destroy` だけの変更では bridge に書き込まない。import 直後と、この属性がない古い state では `false` として扱う

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
      mirek       = provider::hue::mirek_from_kelvin(2700)
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
| `palette` | Optional, Computed, object | ダイナミック再生で順に使う色・色温度・明るさ・エフェクト。下記 |

action object:

| 属性 | 種別 | 説明 |
|---|---|---|
| `on` | Optional | 点灯状態 |
| `brightness` | Optional | 0.0〜100.0 |
| `mirek` | Optional | 色温度（50〜1000 mirek、API全体の範囲） |
| `kelvin` | Optional, **0.2.0 で削除** | 書くとエラーになる。代わりに `mirek` と `provider::hue::mirek_from_kelvin` を使う |
| `color_xy` | Optional, object `{x, y}` | CIE xy |
| `gradient` | Optional, Computed, object | グラデーション対応ライトの色。`points`（2〜5 個の `{x, y}`）と `mode`。下記 |
| `effects_v2` | Optional, Computed, object | エフェクト。`effect` と `parameters`（`color_xy`・`mirek`・`speed`）。下記 |
| `dynamics` | Optional, Computed, object | シーンへの遷移。`duration`（ミリ秒）。下記 |
| `effects` | Optional, Computed, JSON 文字列 | API で非推奨のエフェクト。JSON のまま扱う |

- 色温度は `mirek` で書く。ケルビンで書くときは `mirek = provider::hue::mirek_from_kelvin(2700)` のように変換する
- `kelvin` は 0.2.0 で削除した（互換性のない変更）。入れ子の属性では、Terraform は知らない属性を黙って捨てる。属性を消すだけでは、書いたままの `kelvin` が無視され、色温度を消す plan になる。そのため `kelvin` は属性として残し、書くとエラーにする。エラーは置き換え方（`mirek = provider::hue::mirek_from_kelvin(2700)`）と、`hue-tf migrate` で書き換えられることを示す。deprecated の印も付ける。Terraform は deprecated の属性を生成する設定に書かないので、import で作る設定に `kelvin = null` が出ない
- 保存済みの state では、state upgrader（schema version 4）が `kelvin` を消す。v0.1 は `kelvin` を書いたときも実機の mirek を `mirek` に入れているので、それを残す
- `color_xy` と `mirek` は Computed にしない。plan には書いた値だけが出て、書いていない色や色温度は送らない
- ライトは色か色温度のどちらか一方で光るため、同じアクションに `color_xy` と `mirek` は書けない（検査で止める）。実機のシーン 1,516 アクションに、両方を持つものはなかった
- 色は `color_xy` と独立した `brightness` で指定する。`color_hex` は廃止し、state schema version 1 で旧属性だけを除去して xy を保持する。
- `palette` は Optional + Computed の型のある属性。API の palette に合わせ、中身が 1 つの入れ物は actions と同じく平らにする（`color_xy`・`mirek`・`brightness`）。`color` は 9 個まで、`color_temperature` と `dimming` は 1 個まで、`effects_v2` は 3 個までを plan 時に検査する。明示時は書いたリストを送り、書かなかったリストは空にする。未指定時は実機の値を維持する。actions とは独立して管理し、相互の自動生成は行わない。`effects_v2` の各要素は actions の `effects_v2` と同じ形にする。非推奨の `effects` は扱わず、ほかのアプリが `effects` を書いても差分にしない
- `palette` は 0.1 では JSON 文字列で、0.2.0 で型のある属性に変えた（互換性のない変更。決定記録 14）
  - `palette = jsonencode(...)` と書いた設定は、Terraform の型エラー（`Inappropriate value for attribute "palette": object required.`）になる。エラーは設定を読む時点で出るので、bridge には何も送られない。書き換えには `hue-tf migrate` を用意する
  - 保存済みの state は、state upgrader（schema version 4）が bridge に接続せずに変換する。形は import で bridge から読むときと同じで、空のパレットは null になり、非推奨の `effects` は持たない。v0.1 の state にはパレットを書いていないシーンにも `palette` が入っているが、それも同じく変換する
  - 書き換えた設定の数値が同じなら、上げたあとの plan は変更なしになる。Terraform CLI で v0.1 の形の state から上げる流れを受け入れテストで確かめている
- actions の `gradient`・`effects_v2`・`dynamics` も 0.1 では JSON 文字列で、0.2.0 で `palette` と同じやり方で型のある属性に変えた（決定記録 15）。名前は API のまま変えない
  - 中身が 1 つの入れ物は平らにする。`gradient` の点は `{ color = { xy } }` ではなく `{ x, y }`、`effects_v2` は API の `effects_v2.action` の中身。`dynamics` は `duration` だけだが、`duration` だけでは何の時間かわからないので、入れ物を残す
  - `jsonencode(...)` と書いた設定は Terraform の型エラー（`attribute "gradient": object required.` など）になる。state は upgrader（schema version 4）が import と同じ形に変換し、読めない値は null にして refresh で bridge から読み直す
  - `gradient.mode` は API と同じく `interpolated_palette` を既定値にする。v0.1 の state に mode がなければ既定値で埋める
  - schema version 3 は開発版だけが書いた。その state も、palette の `effects` を `effects_v2` に直して変換する
- `actions` は Map なので、要素の追加・削除・変更は light ごとに独立した差分として表示される

## 9. 色と色温度の変換

### 方針

- bridge 側の正の値は xy と mirek。HCL の色は xy、色温度は mirek で書く。ケルビンは provider 定義関数 `provider::hue::mirek_from_kelvin` で mirek に変換する
- 比較（差分判定）は必ず xy 空間・mirek 空間で行う。kelvin 同士を比較しない
- 変換は `internal/color` パッケージに純粋関数として実装し、table-driven test で検証する

### 色の表示

- `.tf` は xy と brightness を保持する。表示のために元データを RGB へ置換しない。
- `hue-tf preview` は Terraform plan JSON を読み、ターミナルでは正規化した RGB の色見本を表示する。
- HTML は CSS XYZ の色見本と、明るさを近似的に反映した色見本を並べる。照明の測定輝度とディスプレイの輝度は同一とみなさない。
- `show` の hex は表示用の近似値であり、provider の設定・state 属性ではない。

### kelvin ↔ mirek

- `mirek = round(1_000_000 / kelvin)`、`kelvin = round(1_000_000 / mirek)`
- `provider::hue::mirek_from_kelvin` は、API の範囲 50〜1000 に入らない kelvin をエラーにする。0.1 の `kelvin` 属性はこの範囲に収めて送っていた。そのため `kelvin` を書いたときのエラーは、範囲外の kelvin には収めた mirek の数値を示し、範囲内なら関数と数値の両方を示す（関数は Terraform 1.8 以降）

### provider 定義関数

- `provider::hue::mirek_from_kelvin(kelvin)` は `round(1_000_000 / kelvin)` を返す。結果が API の範囲 50〜1000 の外ならエラーにする。Terraform 1.8 以降で使える
- 関数名は `<返すもの>_from_<入力>` とし、引数の単位を名前に含める
- 関数の結果は mirek の数値なので、state・比較・import は `mirek` を直接書いた場合と同じになる
- `provider::hue::color_from_hex(hex)` は `#rrggbb` か `#rgb` を受け取り、`{ color_xy, brightness }` を返す。actions では `merge(provider::hue::color_from_hex("#ff8800"), { on = true })` と書く。merge は後ろの引数が勝つので、関数を先に書くと、明るさなどを自分で書いた値で上書きできる
  - xy は Philips の Wide RGB D65 の式（sRGB のガンマ補正のあと Wide RGB D65 の行列）で求め、小数 4 桁に丸める。sRGB として読むと、hex で Hue の色域の端（特に緑と赤）に届かないため
  - 明るさは RGB で一番大きい値（HSV の V）を百分率にし、小数 2 桁に丸める。輝度（Y）は、実機で青などの飽和した色が暗くなりすぎたため使わない（青の 100% は白の 100% よりもともと暗い）
  - 黒（`#000000`）は色を持たないためエラーにする

### bridge が保存する値

- bridge はシーンの xy と mirek を、light の色域や対応範囲（`mirek_min` / `mirek_max`）の外でも送ったとおりに保存する。明るさは小数 2 桁に丸めて保存する（2026-10-17 に実機で確認。#25・#28）
- provider は xy を小数 4 桁、明るさを小数 2 桁に丸めて送る。palette の xy と明るさも同じ桁に丸めて送る（bridge が palette の明るさをどう保存するかは未確認だが、丸めて送れば保存値と比べ方がそろう）
- palette の `effects_v2` は送ったとおりに保存する。parameters に色と色温度を一緒に書いても受け付け、書かなかった parameters は埋めない。非推奨の `effects` は空のまま（2026-10-17 に実機で確認）
- light がその値をどう表示するかは確認していない

### semantic equality

- 設定値に送るときと同じ変換をした値と、bridge の値を比べる。light の色域や対応範囲は使わない
- `color_xy`: 小数 4 桁に丸めた設定値と bridge の値の差が x, y ともに 0.001 以内なら等しいとみなす
- `mirek`: 設定値と bridge の値の差が ±1 以内なら等しいとみなす
- `brightness`: 小数 2 桁に丸めた設定値と bridge の値が同じなら等しいとみなす
- 等しい場合、state には設定側の表現（ユーザーが書いた xy / mirek / brightness）をそのまま保持する
- 等しくない場合（drift）、state の値を実機の値へ更新する
- scene の Read はシーンだけを読む。light の一覧は取得しない

## 10. 補助 CLI `hue-tf`

`cmd/hue-tf/` に置き、Hue クライアントを provider と共有する。

### v0 で実装するコマンド

| コマンド | 内容 |
|---|---|
| `hue-tf init` | mDNS で bridge を探索し、リンクボタン押下を待って application key を発行する。結果を環境変数の形式で表示する（`export HUE_BRIDGE_HOST=…` / `export HUE_BRIDGE_ID=…` / `export HUE_BRIDGE_APPLICATION_KEY=…`）。bridge ID は mDNS の TXT レコード（`bridgeid`）、なければ証明書の CN から得る |
| `hue-tf discover` | mDNS で bridge を探索し、見つかったアドレスと bridge ID を表示する。application key は発行しない |
| `hue-tf ls <type>` | room / zone / scene / light / device / behavior_instance / behavior_script / button、および switch の対応表を表形式または `--json` で一覧表示する |
| `hue-tf raw <path>` | 任意の v2 エンドポイントに GET し、レスポンスをそのまま出力する |
| `hue-tf version` | バージョンを表示する。リリースビルドでは `-X main.version` で埋め込む。`--help` でコマンドの一覧を表示する |

### v1 以降の候補

- `hue-tf import-blocks`: 未管理リソースの import ブロックを生成する。resource 定義は Terraform の設定生成または手書きに任せる（詳細は第16節）。
- `hue-tf preview [PLAN_JSON] [--html]`: 評価済み設定と差分を色見本として表示
- `hue-tf migrate [DIR ...] [--write]`: 0.1 の設定を 0.2.0 の形に書き換える（第19節）
- `hue-tf recall SCENE_UUID [--action ACTION]` と `hue-tf identify DEVICE_UUID_OR_LIGHT_UUID` は実装済み。明示的な実行時操作として Bridge に PUT し、Terraform 定義・state は変更しない。詳細は [実行時操作](runtime-commands.md) を参照。

### 認証情報の受け渡し

- CLI も `HUE_BRIDGE_HOST` / `HUE_BRIDGE_ID` / `HUE_BRIDGE_APPLICATION_KEY` を読む。provider と同じ

## 11. テスト

- unit test: 色変換、semantic equality、クライアントの rate limit / リトライは純粋関数または httptest で検証する
- acceptance test: `internal/fakebridge` に v2 の `/clip/v2/resource/*` を模したフェイクサーバーを実装し、`TF_ACC=1` のテストを CI（GitHub Actions）でフェイクに対して実行する
- フェイクのレスポンスは、実機から取得した JSON を fixture として持ち、可能な限り実機の挙動（シーンの値をそのまま保存し明るさだけ小数 2 桁に丸めること、404 / 429 の形式）に合わせる
- テスト専用の実機 bridge は持たない。作者の自宅 bridge に対する確認は手動で行い、自動テストの対象にしない

## 12. リリースと公開

- GoReleaser + GPG 署名。terraform-provider-scaffolding-framework のワークフローを踏襲する
- ドキュメントは `tfplugindocs` で schema から生成する
- 公開先は Terraform Registry（namespace `akr4`）と OpenTofu Registry
- CLI のバイナリは provider のリリース成果物とは分けて、同じリポジトリの `hue-tf-vX.Y.Z` リリースで配布する（GoReleaser の設定も分離する）。Registry は `vX.Y.Z` リリースのファイルをすべて provider の成果物として扱うため
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
| 6 | 色・色温度の属性 | xy と brightness を独立に指定し、色温度は mirek を受ける（13）。比較は xy / mirek 空間 | 色情報の保持と可視化を分離する。色見本は preview が担当し、provider は実機補正との同値判定を行う |
| 7 | TLS | Signify CA を同梱して検証し、CN を bridge ID と照合する。insecure なし | 同じ LAN にある別のブリッジへの接続を防ぐ。公開前レビューの指摘で CN 照合を追加した |
| 8 | rate limit | GETは4並列・送信ペース自動調整、書込は直列、429リトライあり | 利用者に `-parallelism` を意識させない |
| 9 | Hue クライアント | 自前で薄く書く | provider の都合に合わせた型にする |
| 10 | CLI の置き場・名前 | 同一リポジトリ `cmd/hue-tf` | クライアントの共有と開発速度を優先 |
| 11 | テスト | フェイク bridge で CI | テスト専用の実機がない |
| 12 | provider 設定 | `host` / `bridge_id` / `application_key`、環境変数 prefix `HUE_BRIDGE_` | |
| 13 | 色温度の入力 | 0.2.0 で `kelvin` 属性を削除し、`mirek` と `provider::hue::mirek_from_kelvin` で書く。`kelvin` は書くとエラーになる属性として残す | 色温度の書き方を場所によらずそろえる。palette の中では mirek しか書けない。入れ子の属性では Terraform が知らない属性を黙って捨てるので、エラーで置き換え方を示す。0.x なので minor（0.2.0）で入れる |
| 14 | palette の型 | 0.2.0 で `palette` を JSON 文字列から型のある属性に変える。名前は変えず、古い書き方はエラーにする | HashiCorp の provider design principles は、スキーマを API の名前と構造に従わせる。名前を変えても、古い名前を消すときに互換性のない変更になるので避けられない。名前を変えると、その間は同じものに名前が 2 つでき、両方書いたときのエラーや、書いていないパレットをどちらで記録するかの仕組みが要る。0.x なので minor（0.2.0）で入れる。state は upgrader が変換し、設定は `hue-tf migrate` で書き換える |
| 15 | actions の gradient・effects_v2・dynamics の型 | 0.2.0 で JSON 文字列から型のある属性に変える。名前は API のまま。palette のエフェクトのリストも API と同じ `effects_v2` にする。API で非推奨の `effects` は JSON 文字列のまま | palette（決定記録 14）と同じ理由。互換性のない変更を 0.2.0 の 1 回にまとめる。actions と palette の `effects_v2` は同じ形なので、名前もそろえる。型のある palette はまだリリースしていないので、名前を変えても困る人はいない |

## 15. 未決定・保留

- 対応する Terraform の版を明記するか。README と Registry への記載、例への `required_version`、CI での 1.8 系のテストが候補。provider から版を強制する仕組みはなく、関数を使う設定は 1.8 より前の Terraform では読み込みで失敗する
- `hue-tf import` の HCL ラベル命名規則
- rate limit の固定値の最終調整（実機での測定後）
- light の設定（name / powerup）を管理する `resource "hue_light"` の要否（v1）

## 16. Import ブロック生成

`hue-tf import-blocks` は未管理の room・zone・scene・smart_scene・behavior_instance を発見し、通常の Terraform import ブロックを準備する。

- 引数なしは Bridge 全体の未管理リソースを対象にする。複数 UUID で限定できる。
- state と既存 import ブロックの UUID を照合し、重複生成を防ぐ。
- state と設定は読み取るだけなので、構成の書き方を制限しない。count・for_each のインスタンス、deposed、provider の別名、対象外の hue_device、共有・リモート source の module、override、`.tf.json` を含んでも動作する。state は1回だけデコードし、module ツリーは1回だけたどる。
- 既存の import ブロックは、Terraform が許す root module からのみ読み取る。
- `--module` の配置先は、ローカル source で、自身と上位に count・for_each のない module に限る。
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
実行状態と画像は読み取り専用。Bridge は新しい smart_scene をすぐ稼働させ、その時間帯のシーンをライトに反映するため、作成時は `recall.action = deactivate` を送って停止状態で作る。更新時は recall を送らず、稼働状態を変えない。画像変更は送信しない。
import-blocks は未管理分の import ブロック準備に対応する。既存定義の変更・衝突検出は行わない。
詳細は [スマートシーン](smart-scenes.md) を参照する。

現在の設定項目ごとの対応範囲・未対応機能・確認根拠は[設定機能の対応表](feature-coverage.md)を参照。

### シーン画像の管理境界

通常シーン・スマートシーンの画像は管理対象外とする。作成・更新時に `metadata.image` を送信せず、既存画像の再設定や `null` による消去も行わない。
旧stateの `image_id` はschema移行で取り除くが、Bridgeへの操作は行わない。既存の `.tf` に `image_id` がある場合は、その属性を削除する。

### シーンのグラデーション・エフェクト・遷移時間

`actions` の `gradient`・`effects_v2`・`dynamics` はOptional + Computedの型のある属性とする。
`gradient` は2〜5個のCIE xyの `points` と `mode`、`effects_v2` は `effect` と `parameters`（`color_xy`・`mirek`・`speed`）、`dynamics` はミリ秒単位の `duration` を表現する。
明示指定はその値を管理し、省略時はBridgeから読み取った値を保持して更新リクエストに含める。
省略した値は state の値を丸めずに送る。Hueアプリはgradientの点を小数7桁まで保存するので、Bridgeから読んだ値はそのまま戻る。書いた値と読んだ値は state では見分けられないため、書いた値を設定から外すと、それまで丸めて送っていた値が丸めずに送られる（差は0.00005未満）。
属性の削除は解除指示ではない。エフェクト停止には `effect = "no_effect"`、即時遷移には `duration = 0` を指定する。
点の数、mode とエフェクトの名前、parameters と duration の範囲は plan 時に検査する。機種固有の内部制約はBridgeで検証する。
Bridgeから読んだ値は、providerが送るときの形に直して比べる。providerが扱わないフィールドは差分にしない。更新では actions 全体を送るので、そうしたフィールドは消える。公式資料のフィールドはすべて扱っている。
API で非推奨の `effects` はJSON文字列のまま扱い、JSON objectであることを検証する。

有効な `effects_v2.effect` と、同じアクション直下の `color_xy` / `mirek` / `gradient` の併用を検証で拒否する。エラーは `effects_v2.effect` を指す。エフェクト内の色指定は `effects_v2.parameters` に置く。`no_effect` の併用可否は未検証のため、この事前検証の対象外とする。実機の 1,516 アクションでは、エフェクト（旧 `effects`）を持つ 6 つはどれも色・色温度・グラデーションを持たず、グラデーションを持つ 21 はどれもエフェクトと色を持たなかった。

色温度の入力検証と `provider::hue::mirek_from_kelvin` の範囲には、API範囲50〜1000 mirekを使う。bridge は light の範囲の外の値もそのまま保存するため、等価比較に `mirek_schema` は使わない。

## 19. 設定の移行（`hue-tf migrate`）

`hue-tf migrate [DIR ...] [--write]` は、0.1 の設定を 0.2.0 の形に書き換える。手順は [0.2.0 への移行](guides/upgrade-0.2.0.md) を参照する。

- 指定したディレクトリ（省略時はカレント）で Terraform が読む `.tf`（隠しファイルとエディタのバックアップを除く）と、そこから呼んでいるローカルのモジュールのうち、指定したディレクトリの中にあるものを読む。モジュールが呼ぶモジュールもたどる。ほかのサブディレクトリと Registry のモジュールは読まない
- 指定したディレクトリの外にあるローカルのモジュールは、0.1 のままのほかの設定と共有されているかもしれないので、読まずに案内だけする（`hue_scene` を持つ場合）
- 構文木の上で、各 `hue_scene` に書いてあるものだけを書き換える。周りの式と書式はそのまま残し、`terraform fmt` 済みのファイルは整形済みのまま保つ
- 既定は差分の表示だけで、最後に `--write` 付きの同じコマンドを示す。`--write` で書き換える。同じディレクトリを 2 回指定しても 1 回だけ読む
- 読めないファイルが 1 つでもあれば、どのファイルも変えない。書き込みは import-blocks と同じ `pull.ReplaceFile` で、一時ファイルからの置き換えで行う。通常のファイル以外と、読んだあとに変わったファイルは書かない
- シンボリックリンクは書き換えず、リンク先のディレクトリを示す。そのディレクトリも読むなら（指定したディレクトリでも、たどったモジュールでも）、リンク先のファイルをそこで書き換える。比べるときはパスの中のシンボリックリンクを解決する。リンク先がないリンクは一覧に出して、ほかのファイルは続けて書き換える
- 改行が CRLF のファイルは、書き換えた部分も CRLF にする。書き換えた値の中や、消した行、消したパレットのすぐ上の行のコメントは残らないので、そのことを表示する。すぐ上のコメントを残すと、次の属性の説明に見えてしまうため
- 差分は、両方のファイルに 1 回ずつ出る行（シーンの名前など）で区切ってから、区間ごとに最長共通部分列で求める（patience diff と同じ考え方）。大きな生成設定でも、使うメモリは区間の大きさで決まる
- `palette` と、actions の `gradient`・`effects_v2`・`dynamics` の `jsonencode` を、API の形から 0.2.0 の型の形に直す。中身が 1 つの入れ物を平らにする規則（第8節）と同じ対応で、`for` 式で作ったリストも直す
- 中身のないリストは書かない（書かなくても空で送る）。中身のないパレットは行ごと消し、Terraform がパレットをブリッジに任せる形にする。v0.1 の生成設定は、パレットのないシーンにも空のパレットを書いているため。空のまま管理したいときは `palette = {}` と書くよう案内する
- `kelvin = K` は `mirek = provider::hue::mirek_from_kelvin(K)` にする。関数の範囲外の数値は、0.1 が送った mirek の数値にする。Terraform 1.8 より前の利用者は数値で書く（kelvin のエラー文が数値を示す）
- 0.1 の生成設定にある `kelvin = null` と、数値の `mirek` と並んだ `kelvin` は消して `mirek` を残す。0.1 は null でない方を送っていたので、`mirek` が式のときはどちらが使われていたか分からず、書き換えずに一覧に出す
- 式の kelvin を包んだ `mirek_from_kelvin` は、kelvin が null だと plan で失敗する。表示でそのことを伝え、ガイドに null を通す書き方を載せる
- 変数・local などから来る値、文字列の JSON、JSON 構文のファイル、非推奨の `effects` に中身があるパレットは、書き換えずに一覧に出す
- `hue_scene` の外で `palette` や actions の `gradient`・`effects_v2`・`dynamics`・`kelvin` を読んでいるところ（`jsondecode(hue_scene.x.palette)` など）も一覧に出す。0.2.0 で型が変わり、plan で失敗するため
- v0.1 の state と設定から migrate で書き換え、plan が変更なしになることを Terraform CLI の受け入れテストで確かめている
