# 実機での確認手順

ローカルビルドの provider と CLI を使い、既存の Hue 構成を読み取り、
Terraform に import して差分を確認する手順です。新しいバージョンの実機確認にも使用します。

## 1. 確認用ディレクトリを準備する

リポジトリのルートで実行します。

```sh
make build
python3 scripts/prepare-bridge-check.py
```

準備スクリプトは `.local/bridge-check` に設定テンプレートと環境変数設定用の `env.sh`を配置します。
再実行しても既存ファイルは上書きしません。テンプレートの変更を取り込む場合は、
`examples/bridge-check` と比較して反映してください。

- 個別の Terraform CLI 設定で、このリポジトリの `bin` を `dev_overrides` に指定します。
- ユーザー全体の Terraform CLI 設定は変更しません。
- state、取得した JSON、実環境のリソース定義は `.local/bridge-check` に置きます。
  `.local` は Git 管理対象外です。
- `env.sh` は `TF_CLI_CONFIG_FILE`、`TF_DATA_DIR`、`TF_WORKSPACE=default` を設定します。
  読み込み後は確認ディレクトリで通常の `terraform` コマンドを実行します。
  application key はファイルに保存しません。

以降は同じターミナルで進めます。

```sh
repo_dir=$(pwd)
cd "$repo_dir/.local/bridge-check"
source ./env.sh
terraform validate
```

環境変数は現在のシェルに適用されます。新しいターミナルでは `env.sh` を再度読み込みます。
別の Terraform プロジェクトに移る場合は新しいターミナルを使うか、これらの変数を解除してください。

`Provider development overrides are in effect` という警告は、この設定では想定した表示です。
`terraform init` は不要です。公開前の provider を Registry から取得しようとせず、
[development override](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers)
を使用します。

## 2. 接続先と認証情報を設定する

既存の application key がある場合は、それを使用します。以下は macOS の zsh 用です。
キーは入力時に表示せず、シェルのコマンド履歴にも直接書き込みません。

```sh
export HUE_BRIDGE_HOST='192.168.1.10' # 実際の IP またはホスト名に変更
read -rs 'HUE_BRIDGE_APPLICATION_KEY?Hue application key: '
printf '\n'
export HUE_BRIDGE_APPLICATION_KEY
```

キーがない場合のみ、ユーザー自身で以下を実行し、bridge のリンクボタンを押します。
この操作は bridge に application key を新規発行します。

```sh
"$repo_dir/bin/hue-tf" init
```

表示された key を、上記の非表示入力で環境変数に設定します。
`HUE_BRIDGE_HOST` が設定されていれば探索を省略します。
mDNS 探索自体も確認する場合は、`init` の実行前に `unset HUE_BRIDGE_HOST` を実行し、
結果に表示された host を設定します。

## 3. 接続と一覧取得を確認する

```sh
"$repo_dir/bin/hue-tf" ls light
"$repo_dir/bin/hue-tf" ls device
"$repo_dir/bin/hue-tf" ls room
"$repo_dir/bin/hue-tf" ls zone
"$repo_dir/bin/hue-tf" ls scene
```

確認する点:

- 証明書エラーや認証エラーなしに取得できること。
- Hue アプリと名前・UUID の対応が確認できること。
- room / zone / scene がない場合、空の一覧が返ること。

エラー時はここで止めて、コマンド名とエラー内容を確認します。
TLS 検証を無効にするオプションはありません。キーは共有しないでください。

選んだリソースの詳細は GET で保存します。次の UUID は実際の値に置き換えます。

```sh
umask 077
"$repo_dir/bin/hue-tf" raw /clip/v2/resource/room/ROOM_UUID > captures/room.json
"$repo_dir/bin/hue-tf" raw /clip/v2/resource/zone/ZONE_UUID > captures/zone.json
"$repo_dir/bin/hue-tf" raw /clip/v2/resource/scene/SCENE_UUID > captures/scene.json
"$repo_dir/bin/hue-tf" ls light --json > captures/lights.json
"$repo_dir/bin/hue-tf" ls device --json > captures/devices.json
```

存在する種類だけ取得します。`raw` は `errors` / `data` を含む API 応答、
`ls --json` はリソースの配列を返します。
取得データを fixture にする場合は、名前や UUID などを匿名化し、参照の整合性と
フィールドの省略・null・false・0 の区別を保ってください。認証情報や `init` の出力は含めません。

## 4. Data source を確認する

確認したい light / device を、`data-sources.tf` などの `.tf` ファイルに直接定義します。
この段階では resource の定義はまだ追加しません。

```hcl
data "hue_light" "desk" {
  id = "実際の light UUID"
}

data "hue_device" "desk" {
  id = "実際の device UUID"
}

output "desk_light" {
  value = data.hue_light.desk
}

output "desk_device" {
  value = data.hue_device.desk
}
```

```sh
terraform plan
```

output で名前、device_id、対応色域、色温度範囲などを確認します。
この段階では output の追加差分は想定内です。bridge 上のリソースの作成・変更・削除は
計画に含まれません。apply せずに確認できます。

## 5. 既存リソースの設定を書く

取得した JSON を見ながら、`bedroom.tf` などの `.tf` ファイルに、
`hue_room.bedroom` / `hue_scene.reading` のような意味のある名前で直接定義します。
同じディレクトリの `resources.tf.example` から必要なブロックをコピーできます。
`.example` のままでは Terraform に読み込まれません。
リソース定義は `.tf` に置き、tfvars は環境ごとに変える入力値が必要な場合に使います。

```hcl
resource "hue_room" "bedroom" {
  name      = "寝室"
  archetype = "bedroom"
  children  = ["実際の device UUID"] # 既存の children を全件記入

  lifecycle {
    prevent_destroy = true
  }
}
```

同じ構成で管理する scene の `group` には `hue_room.bedroom.id` を指定します。

| 種類 | 設定に転記する値 |
|---|---|
| room | `metadata.name`、`metadata.archetype`、**全** `children[].rid`（device UUID） |
| zone | `metadata.name`、`metadata.archetype`、**全** `children[].rid`（light UUID） |
| scene | `metadata.name`、`group.rid`、`speed`、`auto_dynamic`、**全** `actions` |

scene の `actions` は `target.rid` を map のキーにします。

| API の action 内の値 | Terraform の action 内の属性 |
|---|---|
| `on.on` | `on` |
| `dimming.brightness` | `brightness` |
| `color.xy` | `color_xy = { x = ..., y = ... }` |
| `color_temperature.mirek` | `mirek` |

最初の比較では API の正の値である xy / mirek を使います。
hex / kelvin は同時に指定しません。API にない属性は省略し、false と 0 は省略しません。
色温度が null の場合は `mirek` を省略します。
`metadata.image.rid` は `image_id` に記入するか、未指定のまま import で読み取れます。
`palette` は省略すると実機の値を維持します。配色を管理する場合は `jsonencode` で明示します。

scene の所属 group は既存の UUID を直接指定できます。
その room / zone を同時に Terraform 管理対象にする必要はありません。

## 6. Import して No changes を確認する

[CLI の import](https://developer.hashicorp.com/terraform/cli/commands/import) を使い、
選んだリソースを確認用のローカル state に読み込みます。
この provider の import は bridge を GET で読み取り、room / zone / scene を変更しません。
既存の運用 state と、この確認用 state を並行して apply に使用しないでください。

以下の `ROOM_UUID` などを実際の UUID に置き換え、リソース名も `.tf` の定義に合わせます。
設定に追加した種類だけ実行します。

```sh
terraform import hue_room.bedroom ROOM_UUID
terraform import hue_zone.work ZONE_UUID
terraform import hue_scene.reading SCENE_UUID
terraform state list
```

リソース差分だけを確認するため、手順 4 の確認用 output を削除します。
参照していない確認用 data source も削除できます。設定したすべてのリソースが
import 済みであることを確認してから実行します。

```sh
terraform plan -no-color -detailed-exitcode > captures/plan.txt 2>&1
plan_status=$?
cat captures/plan.txt
printf 'plan exit code: %s\n' "$plan_status"
```

[終了コード](https://developer.hashicorp.com/terraform/cli/commands/plan#detailed-exitcode) は
`0` が差分なし、`1` がエラー、`2` が差分ありです。
`No changes` と終了コード `0` が、この確認の完了条件です。

actions / children の不足、名前、archetype、auto_dynamic、group の違いがある場合は、
取得した JSON と設定を照合します。未 import のリソースは作成として表示されます。
差分を解消するために apply するのではなく、まず設定と provider の読み取り処理を確認します。
`prevent_destroy` は設定済みリソースの削除・再作成を抑止しますが、設定自体を取り除いた場合や
通常の更新を全面的に防ぐ仕組みではありません。

この手順に apply / destroy は含みません。書き込み動作の実機確認が必要になった場合は、
変更対象・変更内容・戻し方を別途確認し、ユーザー自身が実行します。

## Import 済みのリソース名を変更する

名前や `for_each` の構成を変えるときは、対応関係を
[`moved` ブロック](https://developer.hashicorp.com/terraform/language/modules/develop/refactoring)
に記録します。UUID が同じでも、アドレスを変更しただけでは対応は自動で引き継がれません。

```hcl
moved {
  from = hue_room.existing["以前の map キー"]
  to   = hue_room.bedroom
}
```

plan に `has moved` と `0 to add, 0 to change, 0 to destroy` が出ることを確認します。
plan だけでは state の移動は保存されません。state の保存を伴う操作は、
計画した内容を確認して別途実行します。

## 確認結果を共有する

- 実行したコマンドと終了コード
- エラー、または plan の差分
- 必要な場合のみ、対象リソースの匿名化した JSON

application key や state 全体を共有する必要はありません。
