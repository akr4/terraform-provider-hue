# アプリから Terraform への取り込み

`hue-tf pull` は Bridge 上の設定を Terraform に取り込みます。実機への書き込みは行いません。
room・zone・scene・smart_scene・behavior_instance が対象です。新規・変更・削除を state の UUID と照合して判定します。

## 基本操作

Terraform のルートディレクトリから実行します。既存の provider 設定、backend、workspace を使用します。
`HUE_BRIDGE_HOST` と `HUE_BRIDGE_APPLICATION_KEY` は Terraform が使う Bridge と一致させてください。
provider に host / application_key を直接設定する場合は環境変数と同じリテラル値にしてください。変数などの式で指定されている場合は照合できないため停止します。

```sh
hue-tf pull
hue-tf pull --write
```

引数なしでは追加する import ブロック・既存定義の編集・実機から削除済みのリソース・衝突を表示します。
プレビューは `.tf`、state、同期基準を変更しません。`--write` は全対象の検査後に実行し、個別の確認は求めません。
衝突や未対応の式などが一つでもある場合、全体を書き込み前に停止します。

| 実機の状態 | `pull --write` の処理 |
|---|---|
| state に未登録 | 通常の `import` ブロックだけを生成（resource 定義は生成しない） |
| 登録済みで変更あり | 既存定義の値を更新 |
| 登録済みで実機から削除済み | resource 定義と不要な import ブロックを除去 |
| 初回の取り込み | 対応する全リソースの import ブロックを準備 |

pull は state を読み取りますが、変更しません。内部で実行する Terraform コマンドは state の読み取りと `validate` のみです。
定義生成待ちの場合、未定義の import 先があるため validate は行いません。

新規の resource 定義を module に置く場合も、生成する `import` ブロックはルートの `imports_hue.tf` 一つにまとめます。既存ファイルには追記します。
apply 前に再び pull した場合は、このブロックから未取り込みの UUID とアドレスを認識し、import ブロックの重複生成を避けます。
未取り込みの resource 定義は pull で更新せず、取り込み完了後から既存定義として更新します。
pull が扱う Hue の import ブロックは、添字・for_each・provider 指定のないアドレスとリテラルの ID に限ります。

## 取り込み範囲と配置

```sh
# state のアドレスを指定せず、1件だけ取り込む
hue-tf pull RESOURCE_UUID --write

# 既存の完全なアドレスも使用可能
hue-tf pull module.bedroom.hue_scene.evening --write

# 管理済みの対象を module とその子 module に限定
hue-tf pull --module downstairs.washroom
```

新規の import 先は、指定なしなら root のアドレスです。`--module` 指定時はその module のアドレスにします。
module を import 先にする場合も、pull は module 内の resource 定義を生成しません。
**`--module` は Hue の部屋による検索条件ではありません。** UUID 指定なしの場合、新規候補は Bridge 全体の未登録リソースです。
既存リソースは state に記録された配置を維持します。部屋と Terraform module の対応は推測しません。

`--module downstairs.washroom` は `module.downstairs.module.washroom` を指し、module ブロックの `source` を順にたどります。
対象は root 配下のローカル source、単一インスタンス、標準 provider の継承です。
共有 source、リモート source、root 外の source、`count`・`for_each`・provider の明示的な差し替え、JSON/override 設定には対応しません。
一括処理では構成全体がこの条件を満たす必要があります。

生成名には metadata.name を使用し、日本語・英数字・`_`・`-` を保持します。
その他は `_` に変換し、数字で始まる名前には `resource_` を付けます。
同名のリソースやファイルが存在する場合、UUID を接尾辞に付けて衝突を避けます。既存ファイルは上書きしません。
新規定義内の参照式や属性の表現は Terraform の生成結果に従います。


## 既存定義の編集と衝突

room/zone の name・archetype・children、scene の name・group・speed・auto_dynamic・image_id・actions、smart_scene の name・group・week_timeslots・transition_duration、behavior の name・enabled・script_id・configuration が対象です。
smart_scene の実行状態・画像、scene の palette など、読み取り専用属性は `.tf` に生成しません。
scene actions の `gradient` と `effects` は `jsonencode({...})` の JSON オブジェクトとして保持します。
これらを定義から省略した場合、provider は実機から読んだ設定を維持します。JSON 内の項目単位の pull にも対応します。
`dynamics` など、その他の未対応の action 項目がある場合は停止します。
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
- `.tf` だけ変更：保持する。
- 両側で同じ値に変更：衝突なし。
- 両側で別の値に変更：項目の位置を表示して停止する。
- 実機で削除、ローカルで変更：停止する。

基準ファイルは Bridge の接続先、作業ディレクトリ、workspace、state lineage に結び付けます。
異なる環境の基準ファイルがある場合は停止します。環境を戻すか、基準ファイルを退避して再度プレビューしてください。
`.hue-pull-*` を Git の除外対象にしてください。基準やバックアップに実機設定が含まれます。

削除は編集後の構成で参照切れがないことを確認します。残った参照がある場合は、先にその参照を整理してください。
module output 経由の参照は削除対象との厳密な対応を解決できないため、保守的に停止します。

## 中断と復旧

書き込み前に `.hue-pull-transaction` にファイルの復旧情報を保存します。
ファイル編集後、定義生成待ちでなければ Terraform の validate を実行します。失敗時は、同時編集がない限りファイルを元に戻します。
強制終了や同時編集で復旧できなかった場合は、復旧情報を残して次の pull を停止します。

中断時は `manifest.json` の対象とファイル差分を確認し、バックアップを参考にファイルを修復してください。
対応がそろったら transaction ディレクトリを退避し、`pull` のプレビューから再開します。
この処理は state を変更せず、state のバックアップも作りません。

Bridge の読み取りとファイル更新は一つの原子的な処理ではありません。
pull 実行中はアプリでの編集や別プロセスでの apply を避けてください。

## 役割の境界

hue-tf は実機リソースの発見、import ブロックの準備、既存定義へのアプリの変更・削除の反映と衝突検出を担当します。
新規 resource 定義の生成、state への取り込み、実機への変更反映は Terraform の担当です。pull はこれらを実行しません。

Hue scene の設定生成では、併用不可の `mirek` と `kelvin`、`color_xy` と `color_hex` の両表現が出力される場合があります。
hue-tf は生成結果を自動修正しません。
過去の hue-tf が生成した resource 定義も、既存の定義として扱い、自動削除しません。

## 互換用の構文

従来の `pull --new [UUID ADDRESS [--write]]` は互換用に残しています。
UUID 指定時は通常フローと同じく import ブロックのみ生成します。引数なしは未管理リソースの一覧です。
新しい通常フローでは `--new` は不要です。
