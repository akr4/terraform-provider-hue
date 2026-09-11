# スイッチの設定管理

`hue_behavior_instance` はボタン・ダイヤル割り当ての作成・import・更新・削除に対応します。
機器のペアリングはアプリで行います。ペアリング済みの機器なら、割り当ては `.tf` から作成できます。

## 対象を特定して取り込む

認証用環境変数を設定した root Terraform ディレクトリで実行します。
1Password を使う場合は、各コマンドに `op run --env-file=.env.op --` を付けます。

```sh
hue-tf ls switch
hue-tf ls switch --json
hue-tf ls behavior_instance
hue-tf ls behavior_script
```

`ls switch` は button / relative_rotary サービスを持つ device と、configuration.device で
その device を参照する behavior_instance を対応付けます。機器 UUID、名前、型番、behavior UUID、状態を表示します。
`no v2 assignment` は、この対応関係の割り当てが API v2 で見つからないことを示します。
旧方式のルールや他システムの操作が存在しないことを証明するものではありません。
機器名だけで同定できない場合は、アプリで対象を確認してください。

取り込むのは **behavior UUID** です。device / button / script UUID とは異なります。

```sh
# 新規・既存を自動判定し、新規分の import ブロックを準備する
hue-tf pull BEHAVIOR_UUID --module bedroom
hue-tf pull BEHAVIOR_UUID --module bedroom --write
```

新規の場合、behavior の名前から Terraform のリソース名を生成します。
既存の場合は state のアドレスを維持します。通常の `pull --write` は新規分の import ブロックも生成します。新規の resource 定義は Terraform の `plan -generate-config-out=generated.tf` または手書きで用意し、plan/apply で取り込みを完了します。
Bridge に割り当て自体が存在しない場合は、下記の作成手順を使用します。

## 未設定のスイッチに割り当てを作る

1. `hue-tf ls switch` で対象の device UUID と既存割り当ての有無を確認します。
2. `hue-tf ls button --json` の owner.rid と metadata.control_id で、その機器の各ボタン UUID を確認します。
3. `hue-tf ls behavior_script --json` で機種に対応する script UUID を確認します。
4. `hue_behavior_instance` に name、enabled、script_id、configuration を定義します。

configuration は同型の機器の設定やスクリプト仕様を参考にし、device と各 button は対象機器の UUID を使用します。
room / scene は Terraform の resource.id を参照できます。動作は暗黙に補完されないので、必要なボタンをすべて定義します。
script_id は新規作成時に必須です。import 済みの設定では省略でき、設定した script_id の変更は置換になります。
重要な割り当てには `lifecycle { prevent_destroy = true }` を付け、意図しない削除や置換を防げます。

plan が想定する割り当ての作成だけを示すことを確認し、ユーザー自身で apply します。
同じ device を参照する behavior が見つかると、新規作成を中止して既存 UUID の import を案内します。
これは作成直前の確認であり、他のクライアントとの同時作成を原子的に防ぐものではありません。

API は新規作成用の dry-run を提供しないため、plan は API の受理や実際のボタン動作を検証しません。
apply 後に `ls switch` の状態と実際のボタン操作を確認してください。

## 編集する

主な設定属性は `name`、`enabled`、`script_id`、`configuration = jsonencode({...})` です。
configuration はスクリプト固有の構造をすべて保持します。時間帯、長押し、巡回、ダイヤル、未知の追加項目も
一部だけを抽出せず出力します。`status`、`last_error` は読み取り専用です。
作成時に type・script_id・metadata.name・enabled・configuration を送信し、更新時は metadata.name・enabled・configuration のみを送信します。
実行中の state や dependees は送信しません。
`name` は behavior の名前であり、機器自体の名前ではありません。

シーン参照を変更する例（configuration 内の該当する recall オブジェクト）:

```hcl
recall = {
  rid   = hue_scene.bedroom_late_night.id
  rtype = "scene"
}
```

生成直後は UUID がリテラルです。管理中の room / scene に対応する箇所を resource.id 参照に置き換えることで、
Terraform に依存関係を伝えられます。他のボタンや設定も含め、configuration 全体を定義してください。
特定の機種の構造を別の機種にそのままコピーせず、取り込んだ設定を基に編集します。
有効／無効は `enabled` で変更します。

plan で対象と差分を確認し、ユーザー自身で apply します。更新後は実際のスイッチ操作も確認してください。
ローカルテストは模擬 Bridge 上の import・更新・差分検出を検証するもので、実機動作の保証にはなりません。

## アプリで変更した設定を取り込む

```sh
hue-tf pull module.bedroom.hue_behavior_instance.switch
hue-tf pull module.bedroom.hue_behavior_instance.switch --write
```

name、enabled、script_id、jsonencode configuration が対象です。
変更されたリテラルを項目ごとに取り込み、変更されていない resource.id 参照やコメントを保持します。
両側で同じ項目を異なる値へ変更した場合や、式・コメントを失わず更新できない場合は停止します。
詳細は [取り込み手順](app-to-terraform.md) を参照してください。

## 管理をやめる

resource ブロックを削除して apply すると、実機の割り当ても削除されます。機器自体はペアリング済みのまま残ります。
割り当てを残して管理だけを外すには、resource ブロックを次に置き換えます
（`removed` は Terraform 1.7 以降）。部屋 module の中なら from は module 内の相対アドレスです。

```hcl
removed {
  from = hue_behavior_instance.switch
  lifecycle {
    destroy = false
  }
}
```

無効化だけでよければ `enabled = false` を使用します。

## API 参照

- [OpenHue BehaviorInstancePut schema](https://github.com/openhue/openhue-api/blob/main/src/behavior_instance/schemas/BehaviorInstancePut.yaml)
- [OpenHue BehaviorInstanceGet schema](https://github.com/openhue/openhue-api/blob/main/src/behavior_instance/schemas/BehaviorInstanceGet.yaml)

- [node-hue API reference (create/deleteBehaviorInstance)](https://github.com/rodney42/node-hue#api-reference)

公開 API クライアントの作成・削除メソッド、公開スキーマの取得・更新項目、Bridge が返す既存設定に合わせた実装です。
script-specific configuration の完全なスキーマ検証は Bridge が行います。
