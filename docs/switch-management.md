# スイッチの設定管理

`hue_behavior_instance` は Hue アプリで作成した既存のボタン・ダイヤル割り当てを管理するリソースです。
機器のペアリングはアプリで行います。behavior の新規作成・実機からの削除は対応していません。

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
hue-tf pull --new BEHAVIOR_UUID module.bedroom.hue_behavior_instance.switch
hue-tf pull --new BEHAVIOR_UUID module.bedroom.hue_behavior_instance.switch --write
terraform import module.bedroom.hue_behavior_instance.switch BEHAVIOR_UUID
terraform plan -target=module.bedroom.hue_behavior_instance.switch
```

生成した定義を確認してから import し、`No changes` を確認します。
CLI の `pull --write` はファイルだけを変更し、state や Bridge は変更しません。
新規作成・削除や実際のボタン動作を復旧する必要がある場合は、まずアプリで設定を用意します。

## 編集する

生成される属性は `name`、`enabled`、`configuration = jsonencode({...})` です。
configuration はスクリプト固有の構造をすべて保持します。時間帯、長押し、巡回、ダイヤル、未知の追加項目も
一部だけを抽出せず出力します。`script_id`、`status`、`last_error` は読み取り専用です。
Bridge に送信するのは metadata.name、enabled、configuration のみで、実行中の state や dependees は送信しません。
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

name、enabled、リテラルオブジェクトの jsonencode configuration が対象です。
state と異なるローカル編集があれば上書きせず停止します。
configuration 内に resource.id などの式がある場合も、参照を UUID に置き換えないよう停止します。
変更が必要な configuration 内にコメントがある場合は停止するため、手動で差分を反映してください。
名前や属性末尾など、置換範囲外のコメントは保持します。

## 管理をやめる

削除 apply はエラーになります。アプリの割り当てを残して管理だけを外すには、resource ブロックを次に置き換えます
（`removed` は Terraform 1.7 以降）。部屋 module の中なら from は module 内の相対アドレスです。

```hcl
removed {
  from = hue_behavior_instance.switch
  lifecycle {
    destroy = false
  }
}
```

ユーザーが plan / apply で state から外した後、必要ならアプリで割り当てを削除します。

## API 参照

- [OpenHue BehaviorInstancePut schema](https://github.com/openhue/openhue-api/blob/main/src/behavior_instance/schemas/BehaviorInstancePut.yaml)
- [OpenHue BehaviorInstanceGet schema](https://github.com/openhue/openhue-api/blob/main/src/behavior_instance/schemas/BehaviorInstanceGet.yaml)

公開スキーマの取得・更新項目と、Bridge が返す既存設定に合わせた実装です。
script-specific configuration の完全なスキーマ検証は Bridge が行います。
