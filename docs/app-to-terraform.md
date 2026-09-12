# Import ブロックの生成

`hue-tf import-blocks` は Bridge 上の未管理リソースを発見し、Terraform の `import` ブロックを生成します。
room・zone・scene・smart_scene・behavior_instance が対象です。
resource 定義の生成、既存定義の更新・削除、state の更新、実機への書き込みは行いません。

## 操作

Terraform のルートディレクトリから実行します。既存の backend と workspace の state を読み取ります。
`HUE_BRIDGE_HOST` と `HUE_BRIDGE_APPLICATION_KEY` は Terraform が使う Bridge と一致させてください。
provider に host / application_key を直接設定する場合は環境変数と同じリテラル値にしてください。変数などの式で指定されている場合は照合できないため停止します。

```sh
# 未管理リソースの import ブロックをプレビュー
hue-tf import-blocks

# ルートの imports_hue.tf に生成・追記
hue-tf import-blocks --write

# UUID を指定して対象を限定（複数指定可）
hue-tf import-blocks UUID_A UUID_B --write

# import 先の module を指定
hue-tf import-blocks UUID_A --module downstairs.washroom --write
```

state にある UUID と、既存の import ブロックにある UUID は対象から除外します。
既存リソースは実機で変更・削除されていても、定義や参照を書き換えません。
管理済みの UUID または完全なリソースアドレスを明示指定した場合は、スキップしたことを表示します。
同じ UUID を複数回指定しても生成は一度だけです。指定対象が見つからない場合は、全体の書き込みを停止します。

新規 import ブロックは、配置先が module の場合もルートの `imports_hue.tf` 一つにまとめます。
既存ファイルには追記し、既存ブロックは編集・削除しません。取り込み前の再実行でも重複生成しません。
扱える Hue の import ブロックは、添字・for_each・provider 指定のないアドレスとリテラルの ID に限ります。

## 配置と名前

配置先は既定で root です。`--module downstairs.washroom` は `module.downstairs.module.washroom` を指し、module ブロックの `source` を順にたどります。
**`--module` は Hue の部屋による検索条件ではありません。** UUID 指定なしの場合、候補は Bridge 全体の未管理リソースです。
部屋と Terraform module の対応は推測しません。

対象は root 配下のローカル source、単一インスタンス、標準 provider の継承です。
共有 source、リモート source、root 外の source、`count`・`for_each`・provider の明示的な差し替え、JSON/override 設定には対応しません。
構成全体がこの条件を満たす必要があります。

生成名には metadata.name を使用し、日本語・英数字・`_`・`-` を保持します。
その他は `_` に変換し、数字で始まる名前には `resource_` を付けます。
アドレスが既存定義や別の import 先と重複する場合、UUID を接尾辞に付けて衝突を避けます。

## 書き込みと中断

プレビューではファイルを変更しません。`--write` は全対象の検査後に import ブロックを書き込みます。
内部で実行する Terraform コマンドは state の読み取りと `validate` のみです。
定義生成待ちの場合は未定義の import 先があるため validate は行いません。

書き込み前に `.hue-pull-transaction` にファイルの復旧情報を保存します。この名前は旧コマンドの中断検出との互換性のため維持しています。
失敗時は同時編集がない限りファイルを元に戻します。復旧できなかった場合は復旧情報を残し、再実行を停止します。
中断時は `manifest.json` とバックアップを確認してファイルを修復し、transaction ディレクトリを退避してから再実行してください。
`.hue-pull-*` は Git の除外対象にしてください。

旧 `pull` の比較基準 `.hue-pull-baseline.json` は使用・更新しません。
旧 `pull` コマンドは移行案内を表示して終了します。`import-blocks` は既存リソースの同期を行いません。
`--new UUID ADDRESS` は明示アドレス指定の互換構文として残していますが、通常の一括生成に `--new` は不要です。
