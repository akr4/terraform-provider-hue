# アプリから Terraform への取り込み

`hue-tf pull` は Bridge 上の設定を Terraform に取り込みます。実機への書き込みは行いません。
room・zone・scene・behavior_instance が対象です。新規・変更・削除を state の UUID と照合して判定します。

## 基本操作

Terraform のルートディレクトリから実行します。既存の provider 設定、backend、workspace を使用します。
`HUE_BRIDGE_HOST` と `HUE_BRIDGE_APPLICATION_KEY` は Terraform が使う Bridge と一致させてください。
provider に host / application_key を直接設定する場合は環境変数と同じリテラル値にしてください。変数などの式で指定されている場合は照合できないため停止します。
初回は provider と backend を準備し、ローカル module は `terraform get` で登録してください。

```sh
hue-tf pull
hue-tf pull --write
```

引数なしでは追加する定義・既存定義の編集・実機から削除済みのリソース・衝突を表示します。
プレビューは `.tf`、state、同期基準を変更しません。`--write` は全対象の検査後に実行し、個別の確認は求めません。
衝突や未対応の式などが一つでもある場合、全体を書き込み前に停止します。

| 実機の状態 | `pull --write` の処理 |
|---|---|
| state に未登録 | resource 定義を生成し、`terraform import` で登録 |
| 登録済みで変更あり | 既存定義の値を更新し、refresh-only で state を更新 |
| 登録済みで実機から削除済み | resource 定義と不要な import ブロックを除去し、`terraform state rm` で登録解除 |
| 初回の取り込み | 対応する全リソースを新規取り込み |

state の JSON を独自に書き換えたり `state push` したりはしません。
state の更新には `plan -refresh-only` の保存済み plan を使い、リソース変更がないことを JSON で検査してから apply します。
通常の `terraform apply` は実行しません。出力値や読み取り専用属性は Terraform の refresh に従います。

実機へ `.tf` の変更を送る場合は、従来どおり `terraform plan` と `terraform apply` を使用します。

## 取り込み範囲と配置

```sh
# state のアドレスを指定せず、1件だけ取り込む
hue-tf pull RESOURCE_UUID --write

# 既存の完全なアドレスも使用可能
hue-tf pull module.bedroom.hue_scene.evening --write

# 管理済みの対象を module とその子 module に限定
hue-tf pull --module downstairs.washroom
```

新規リソースは、指定なしなら root に生成します。`--module` 指定時はその module の直下に生成します。
**`--module` は Hue の部屋による検索条件ではありません。** UUID 指定なしの場合、新規候補は Bridge 全体の未登録リソースです。
既存リソースは state に記録された配置を維持します。部屋と Terraform module の対応は推測しません。

`--module downstairs.washroom` は `module.downstairs.module.washroom` を指し、module ブロックの `source` を順にたどります。
対象は root 配下のローカル source、単一インスタンス、標準 provider の継承です。
共有 source、リモート source、root 外の source、`count`・`for_each`・provider の明示的な差し替え、JSON/override 設定には対応しません。
一括処理では構成全体がこの条件を満たす必要があります。

生成名には metadata.name を使用し、日本語・英数字・`_`・`-` を保持します。
その他は `_` に変換し、数字で始まる名前には `resource_` を付けます。
同名のリソースやファイルが存在する場合、UUID を接尾辞に付けて衝突を避けます。既存ファイルは上書きしません。
同じ module にある room/zone は、新規シーンの `group` から直接参照します。

初回は root にまとめて取り込み、その後に Terraform の通常の方法で module に整理できます。
定義を module に移す際は `moved` ブロックを用意し、全体の plan で意図しない追加・更新・削除がないことを確認して apply します。

## 既存定義の編集と衝突

name、archetype、children、scene の group・speed・auto_dynamic・image_id・actions、behavior の name・enabled・script_id・configuration が対象です。
scene の palette など、読み取り専用属性は `.tf` に生成しません。
scene actions に gradient/effects など provider が扱えない項目がある場合は停止します。
`color_hex` は xy から元の値を損失なく復元できないため、`color_xy` を使用してください。

既存の resource 名、module、参照式、コメントを保持し、変更されたリテラルだけを編集します。
`jsonencode` のオブジェクト内も項目ごとに比較するため、スイッチ内の変更されていない参照式はそのまま残ります。
要素の増減によって式やコメントを消してしまう場合、または変更対象の式を解決できない場合は停止します。
変更先が同じ module の既存または同時取り込みのリソースなら、直接的な `resource.id` 参照を新しい参照先へ更新します。
任意の変数、locals、module outputs、関数の評価は行いません。同じ module の直接的な resource 参照は state から解決します。

比較には前回の正常な取り込みを記録した `.hue-pull-baseline.json` を使用します。
初回は現在の state が基準です。初回より前に state が refresh 済みの場合、その前の実機値は復元できません。
以後は通常の Terraform refresh があっても、前回 pull の比較基準を維持します。

- 実機だけ変更：取り込む。
- `.tf` だけ変更：保持する。実機への反映は通常の plan/apply で行う。
- 両側で同じ値に変更：衝突なし。
- 両側で別の値に変更：項目の位置を表示して停止する。
- 実機で削除、ローカルで変更：停止する。

基準ファイルは Bridge の接続先、作業ディレクトリ、workspace、state lineage に結び付けます。
異なる環境の基準ファイルがある場合は停止します。環境を戻すか、基準ファイルを退避して再度プレビューしてください。
`.hue-pull-*` を Git の除外対象にしてください。基準やバックアップに実機設定が含まれます。

削除は編集後の構成で参照切れがないことを確認します。残った参照がある場合は、先にその参照を整理してください。
module output 経由の参照は削除対象との厳密な対応を解決できないため、保守的に停止します。

## 中断と復旧

書き込み前に `.hue-pull-transaction` に state のバックアップとファイルの復旧情報を保存します。
ファイル編集後、Terraform の validate を実行します。state 操作前の失敗は、同時編集がない限りファイルを元に戻します。
state 操作が始まった後の失敗では、状態を推測して巻き戻さず、復旧情報を残して次の pull を停止します。

中断時は `manifest.json` の対象と現在の `terraform state list`、ファイル差分を確認してください。
import 済みの定義を維持し、未完了の import を実行するか、バックアップを参考にファイルと state の対応を修復します。
実機の削除は不要です。state バックアップを無条件に push しないでください。
対応がそろったら transaction ディレクトリを退避し、`pull` のプレビューから再開します。

Terraform は各 state 操作をロックしますが、複数の import 全体や Bridge の読み取りは一つの原子的な処理ではありません。
pull 実行中はアプリでの編集や別プロセスでの apply を避けてください。既存の backup ファイルは復旧確認後に整理できます。

## Terraform 標準機能との違い

`terraform import ADDRESS UUID` は既存リソースを state に登録し、定義は生成しません。
`import` ブロックと `terraform plan -generate-config-out=...` は初回取り込み用の定義を生成できますが、既存 `.tf` の継続的な逆同期ではありません。
この CLI は Bridge 内の対象発見、既存 `.tf` の更新と削除、衝突検出を担い、state 操作は Terraform に任せます。

- [Terraform import コマンド](https://developer.hashicorp.com/terraform/cli/import)
- [import ブロックからの設定生成](https://developer.hashicorp.com/terraform/language/import/generating-configuration)

従来の `pull --new [UUID ADDRESS [--write]]` は互換用に残しています。
この旧形式だけは定義生成のみを行い、表示された import コマンドの別途実行が必要です。
新しい通常フローでは `--new` は不要です。
