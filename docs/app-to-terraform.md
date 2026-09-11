# アプリで変更した設定を Terraform に取り込む

`hue-tf pull` は room・zone・scene の変更を実機から取り込みます。

`hue-tf pull hue_scene.NAME` は、import 済みのシーンについて、Hue アプリで保存した
on/off・明るさ・色温度・色を既存の `.tf` に取り込むコマンドです。
対応する属性は **`on`、`brightness`、`mirek` / `kelvin`、`color_xy.x` / `color_xy.y` の既存リテラル**です。
点灯中のライトの明るさではなく、シーンに保存された action を読み取ります。

## 手順

Terraform の作業ディレクトリで、通常の CLI 設定と `HUE_BRIDGE_HOST` /
`HUE_BRIDGE_APPLICATION_KEY` を設定して実行します。`hue-tf` は PATH 上にあるものとします。

1. 最初に対象のシーンを import し、`terraform plan` で差分がないことを確認します。
2. アプリでシーンの明るさを変更し、シーンを保存します。
3. 変更候補を確認します。

```sh
hue-tf pull hue_scene.bedroom_late_night
```

出力例:

```text
bedroom.tf: hue_scene.bedroom_late_night actions["ライトのUUID"].brightness: 20 -> 10
```

4. `.tf` に取り込みます。実行時に実機とファイルを再取得するため、候補は最新の値になります。

```sh
hue-tf pull hue_scene.bedroom_late_night --write
```

`--write` がない場合、ファイルは変更しません。変更前のファイルは同じディレクトリに
`.hue-pull-backup-*` という名前で保存します。バックアップには元のファイル全体が含まれます。
Bridge と Terraform state への書き込みは行いません。

5. Git 管理している設定なら `git diff` で変更を確認し、`terraform plan` で `No changes` を確認します。
   Git 管理対象外の検証ディレクトリでは、表示されたバックアップと `diff -u` で比較できます。
   差分が残る場合は内容を確認し、通常の apply でアプリの変更を戻さないようにしてください。
6. 次回の比較基準となる state を更新するため、`terraform plan -refresh-only` を確認し、
   ユーザー自身で `terraform apply -refresh-only` を実行します。その後、設定をコミットします。
   refresh-only は `.tf` を更新しないため、取り込み前には実行しないでください。

## 対応範囲と競合

- `hue_scene.NAME` または `module.NAME.hue_scene.NAME` を1件ずつ指定します。UUID は `terraform state pull` の完全なアドレスで対応付けます。
- `.tf` の `actions` と各 action が直接書かれたオブジェクトで、キーを定数として解決できる必要があります。
- 既存の数値・真偽値部分だけを置き換えます。リソース名、group の参照、コメント、空白は保持します。
- xy 座標は provider と同じ小数4桁への丸めと許容差 0.001 で比較し、丸め差だけでは更新しません。
- `.tf` の対象属性が state と異なり、実機とも一致しない場合は、ローカル編集と判断して停止します。
  変数、関数、計算式も上書きしません。1件でも非対応や競合がある場合、そのファイルは変更しません。
- state は前回同期時の比較基準です。先に refresh-only や再 import で更新すると、変更元を判別できなくなります。
- `count`、`for_each`、provider alias、JSON 形式の設定、override ファイルには未対応です。
- 色モード切り替え以外の属性の追加・削除、action の追加・削除、シーンの削除は取り込みません。
- 色温度↔カラーの切り替えは取り込めます。実機で一方だけが保存されている場合、
  `mirek` / `kelvin` → `color_xy`、または `color_xy` → `mirek` に属性を置き換えます。
  行末コメントと他の action は保持します。色オブジェクト内部にコメントがある場合は、
  注釈を失わないよう停止します。カラーから色温度へ戻す際の生成形式は `mirek` です。
- `color_hex` は xy からの逆変換で情報が失われるため未対応です。色を取り込む場合は `color_xy` を使用します。
- 名前、group、speed、palette、gradient、effects などは同期しません。
  このコマンドの「No supported resource changes」は、シーン全体の `No changes` を意味しません。

このコマンドは明示的な取り込み用です。常時同期や競合の自動解決は行いません。

## room・zone の変更を取り込む

```sh
hue-tf pull hue_room.bedroom
hue-tf pull hue_room.bedroom --write
terraform plan -target=hue_room.bedroom

hue-tf pull hue_zone.living
```

room・zone は `name`、`archetype`、`children` に対応します。
room の children は device UUID、zone は light UUID です。
所属の追加・削除を取り込み、順序だけの違いは無視します。名称の引用符や
`${...}` も、Terraform の式として解釈されないようにエスケープします。

3属性を直接定義したリソースが対象です。変数参照や計算式、state と異なるローカル編集があれば停止します。
`children` リスト内にコメントがあり所属変更が必要な場合も、注釈を失わないよう停止します。
属性の後ろのコメントと、`lifecycle` など他の設定は保持します。
プレビュー・バックアップ・state 同期の手順はシーンと共通です。

## アプリで新規作成したリソースを取り込む

既存の Terraform 作業ディレクトリ・workspace で実行します。
`terraform state pull` が成功する設定が必要です。state の読み取りが失敗した場合は、
全リソースを未管理と判断せず停止します。

```sh
hue-tf pull --new
```

現在の state に含まれない room・zone・scene・behavior_instance を種類ごとに UUID・名前付きで表示します。
scene は所属する部屋/ゾーンも表示します。
子 module や `for_each` で import 済みの UUID も除外します。

対象の UUID と、種類に対応する新しい Terraform リソース名を指定して、生成内容を確認します。

```sh
hue-tf pull --new SCENE_UUID hue_scene.bedroom_evening
hue-tf pull --new SCENE_UUID hue_scene.bedroom_evening --write
hue-tf pull --new ROOM_UUID hue_room.guest_room --write
hue-tf pull --new ZONE_UUID hue_zone.downstairs --write
```

`--write` は `<種類>_<リソース名>.tf`（例: `scene_bedroom_evening.tf`）を新規作成し、次に実行するコマンドを表示します。
既存ファイルや同名のリソース定義・state アドレスは上書きしません。
UUID が生成先と同じ module の標準 provider で管理されている room/zone に対応する場合は、
`group = hue_room.bedroom.id` のような参照を生成します。それ以外は UUID を直接記載します。

生成された `.tf` を確認し、表示された import コマンドをユーザー自身で実行してください。
**import 前には plan/apply で新規作成を進めないでください。**

```sh
terraform validate
terraform import hue_scene.bedroom_evening SCENE_UUID
terraform plan
```

`No changes` を確認して管理に取り込みます。`hue-tf` 自身は import や実機への書き込みを行いません。
生成後は、これまでと同じ `hue-tf pull hue_scene.bedroom_evening` で更新できます。

room・zone は name、archetype、children と `prevent_destroy = true` を生成します。
scene は name、group、speed、auto_dynamic、image_id と provider が対応する actions を生成します。
palette は provider の読み取り専用属性なので設定には生成しません。
actions に gradient、effects などの未対応項目がある場合、生成を中止して報告します。


## ローカル module 内のリソース

新規取り込みでは種類と名前を省略できます。種類は Bridge 上の UUID から、名前は
保存された metadata.name から決定します。日本語・英数字・`_`・`-` は保持し、それ以外を
`_` に変換します。数字で始まる場合は `resource_` を付けます。既存の名前と衝突した場合は
停止するため、完全なリソースアドレスで別名を指定してください。

```sh
hue-tf pull --new RESOURCE_UUID --write
hue-tf pull --new RESOURCE_UUID --module washroom --write
hue-tf pull --new RESOURCE_UUID --module downstairs.washroom --write
```

`--module` の省略時は root に生成します。指定値はディレクトリ名や Hue の部屋名ではなく、
root から順にたどる module ブロックの名前です。`downstairs.washroom` は
`module.downstairs.module.washroom` を表します。所属する部屋から配置先は推測しません。
`--module` と完全なリソースアドレスは併用できません。`--write` を省略するとプレビューのみです。
生成後には表示された `terraform import` の実行が必要です。Bridge と state は変更しません。
既存定義の更新には引き続き完全なリソースアドレスを指定します。


Terraform の root ディレクトリから、完全なアドレスで指定します。

```sh
hue-tf pull module.bedroom.hue_room.bedroom
hue-tf pull module.bedroom.hue_scene.evening --write
hue-tf pull --new SCENE_UUID module.bedroom.hue_scene.new_scene --write
terraform plan -target=module.bedroom
```

`source = "./rooms/bedroom"` のようなローカル module の元ファイルを編集します。
ネストした `module.floor.module.bedroom.hue_scene.evening` も指定できます。
生成された import コマンドにも module を含む完全なアドレスを使用します。
同じ module 内の room/zone は直接参照し、別 module の group は UUID で参照します。

対応範囲は root ディレクトリ配下のローカル source、単一インスタンス、標準 provider の継承です。
同一ソースを複数の module 呼び出しで共有している場合、他のインスタンスを巻き込まないよう停止します。
外部/ダウンロード済みソース、root 外の source、module の count/for_each/providers 指定は編集しません。
`terraform get` で module を登録し、既存 state のアドレスを移行してから pull を使ってください。

既存リソースの module 化には `moved` ブロックを使います。移行の最初の plan は target を付けず全体で確認し、
追加・変更・削除がゼロであることを確認したうえで、ユーザー自身が apply してアドレスの移動を state に保存します。

## スイッチの割り当て

`hue_behavior_instance` の新規取り込みと、name・enabled・リテラル jsonencode configuration の pull に対応します。
機器 UUID と behavior UUID の違い、import 手順、参照式やコメントを含む場合の制約は
[スイッチ管理](switch-management.md) を参照してください。
