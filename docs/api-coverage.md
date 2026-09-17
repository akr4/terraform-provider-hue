# Hue API v2とプロバイダーの対応範囲

確認日: 2026-09-17。公開前の不足項目と、公開後の機能拡張を判断するための一覧。
アプリ操作から見た概要は[設定機能の対応表](feature-coverage.md)を参照する。

## 調査範囲と確度

プロバイダーのschemaだけでなく、受信型・更新payload・補助CLIの送信項目を照合した。
API側は[OpenHue公開スキーマの固定コミット](https://github.com/openhue/openhue-api/tree/1ffc817857abf456d5ff2ae50400ef768dbce28e/src)を使用した。
同コミットの各リソースのPUTスキーマ、提供されるPOSTスキーマ、参照先のaction・metadata等を確認している。

これはHue公式仕様そのものではない。[公式v2リファレンス](https://developers.meethue.com/develop/hue-api-v2/api-reference/)は取得時に403となった。
以下の「API項目あり」は**この公開スキーマに書き込み項目がある**という意味であり、全Bridge・全機種での受理を保証しない。
書き込みによる網羅検証はしていない。スキーマ自体にも、実装済みbehaviorのPOST/DELETE定義がないなどの不足がある。
したがって、項目の記載がないことを「Hue APIではできない」という証拠にはしない。

- **専用属性**: Terraformの型付き属性で管理する。
- **JSON対応**: JSON全体を渡せる。各項目のvalidationや専用UIがあるという意味ではない。
- **未対応**: Terraformの設定・取得属性として公開されていない。
- **CLIのみ**: 一回限りの操作として補助CLIで提供する。
- **要確認**: 公開スキーマの不足・矛盾や、実機との対応が未確定。

永続設定、現在の点灯状態を変える操作、情報取得は区別する。未対応一覧は、すべてをTerraform resourceに追加する計画ではない。

## 対応済みリソースの中にある不足

| APIリソース・項目 | 現在の対応 | 不足・制限 |
|---|---|---|
| device.metadata.name / archetype | hue_deviceの専用属性 | 公開PUTのmetadata項目は対応。名前のアプリ反映は利用者確認済み。機種別のarchetypeは未網羅 |
| device.services内のlight参照 | hue_device.light_ids、data hue_device.light_ids | 照明以外のサービスIDは公開しない |
| room / zone: children、metadata.name / archetype | 専用属性 | 照合した書込設定項目の欠落なし。機器の所属変更の競合を独自解決しない |
| scene: metadata.name / image、group、actions、palette、speed、auto_dynamic | 専用属性＋JSON | groupは作成時に指定し、変更は置換。imageは実機が更新を拒否する場合あり |
| scene.actions[].action.on / dimming / color / color_temperature | 専用属性 | 対応。色温度のmirek/kelvin併用は不可 |
| scene.actions[].action.gradient / effects | JSON対応 | 未対応ではない。内部の値は主にBridgeが検証 |
| scene.actions[].action.dynamics.duration | **未対応** | 遷移時間(ms)。Action型にもschemaにもなく、読取・再送で保持しない。[Scene action][action] |
| scene.metadata.appdata | **未対応** | アプリ固有の自由形式文字列。Metadata型にもなく、読取・再送で保持しない。[Scene metadata][scene-metadata] |
| smart_scene.metadata.name、group、week_timeslots、transition_duration | 専用属性 | 公開timeslotのtime/sunsetを表現可能 |
| smart_scene.metadata.appdata | **未対応** | 通常シーンと同じくアプリ固有の文字列。[Smart metadata][smart-metadata] |
| smart_scene.metadata.image（POST） | **読取のみ** | 作成時のimage指定が公開POSTにあるがproviderでは指定不可。PUTのmetadataにはimageがなく、「既存画像を変更できる」とは判断しない。[Smart POST][smart-post] |
| behavior_instance.enabled / configuration / metadata.name | 専用属性＋JSON | 公開PUT項目は対応。script_idは作成・importに対応し変更は置換。スクリプト固有configurationはJSONとして扱える |
| scene / smart_scene: recall | **CLIのみ・一部対応** | actionは対応。通常シーンのrecall.duration、recall.dimmingはCLIでも未対応。[Scene recall][recall] |

**保持の注意:** scene更新はactions全体を組み直して送信する。dynamicsを読み取れないため、既存値を送信に含められない。
実際にBridgeが既存値を消すかは未検証だが、保持を保証できない。appdataも送信しないが、metadataの部分更新で残るかは別の確認事項。
「未対応属性が必ず削除される」とは断定しない。

[action]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/scene/schemas/ActionPost.yaml
[scene-metadata]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/scene/schemas/SceneMetadata.yaml
[smart-metadata]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/smart_scene/schemas/SmartSceneMetadata.yaml
[smart-post]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/smart_scene/schemas/SmartScenePost.yaml
[recall]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/scene/schemas/SceneRecall.yaml

## 書き込み項目があるが未対応の設定

| APIリソース | 公開スキーマ上の書き込み項目 | 用途・境界 |
|---|---|---|
| light | powerup.preset、on / dimming / colorの復帰設定 | 電源復帰時の状態。safety / powerfail / last_on_state / custom。詳細スキーマの入れ子と実機JSONに差があるため、custom構造は追加確認。[Light][light] |
| motion | enabled、sensitivity.sensitivity | 検知の有効無効・感度。behavior.enabled、昼光感度とは別。[Motion][motion] |
| light_level | enabled | 照度測定の有効無効。[Light level][light-level] |
| temperature | enabled | 温度測定の有効無効。[Temperature][temperature] |
| button | metadata.control_id、button.repeat_interval | ボタンの識別番号、repeatイベント間隔。操作割り当てはbehaviorで別途対応済み。[Button][button] |
| contact / camera_motion | enabled | 接触・カメラ検知サービスの有効無効。カメラ全体の設定対応を意味しない。[Contact][contact]、[Camera motion][camera] |
| convenience_area_motion / security_area_motion | enabled | エリア単位の検知サービスの有効無効。[Convenience][convenience]、[Security][security] |
| motion_area_configuration | enabled | モーションエリア構成の有効無効。エリアの作成・照明選択・感度設定まで公開されているとは確認できない。[Motion area][motion-area] |
| service_group | services | サービスのグルーピング。room/zoneとは別。[Service group][service-group] |
| entertainment_configuration | metadata.name、configuration_type | エンターテインメントエリアの名前・用途。照明位置のGETはあるが、公開PUTには位置・メンバー編集項目がなく、その書込仕様は要確認。[Entertainment configuration][entertainment-config] |
| zigbee_connectivity | channel.value | Zigbeeチャンネル11 / 15 / 20 / 25。ネットワーク設定変更に当たる。[Zigbee][zigbee] |
| geofence_client | name | ジオフェンスクライアント名。is_at_homeは現在状態として次表に分離。[Geofence][geofence] |

[light]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/light/schemas/LightPut.yaml
[motion]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/motion/schemas/MotionPut.yaml
[light-level]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/light_level/schemas/LightLevelPut.yaml
[temperature]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/temperature/schemas/TemperaturePut.yaml
[button]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/button/schemas/ButtonPut.yaml
[contact]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/contact/schemas/ContactPut.yaml
[camera]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/camera_motion/schemas/CameraMotionPut.yaml
[convenience]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/convenience_area_motion/schemas/ConvenienceAreaMotionPut.yaml
[security]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/security_area_motion/schemas/SecurityAreaMotionPut.yaml
[motion-area]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/motion_area_configuration/schemas/MotionAreaConfigurationPut.yaml
[service-group]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/service_group/schemas/ServiceGroupPut.yaml
[entertainment-config]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/entertainment_configuration/schemas/EntertainmentConfigurationPut.yaml
[zigbee]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/zigbee_connectivity/schemas/ZigbeeConnectivityPut.yaml
[geofence]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/geofence_client/schemas/GeofenceClientPut.yaml

## 実行時操作・現在状態の変更

これらもAPIとの差だが、通常の永続設定resourceとは分けて公開範囲を判断する。
シーンに保存できることと、light APIへ即時送信できることは同じではない。

| リソース | 項目 | 現在の対応 |
|---|---|---|
| light | on、dimming、color、color_temperature、gradient、effects | 直接制御は未対応。シーンactionsへの保存は対応 |
| light | dimming_delta、color_temperature_delta、dynamics.duration / speed、alert、signaling、mode | 直接制御は未対応 |
| light | effects_v2.action.effect / parameters（color、color_temperature、speed）、timed_effects.effect / duration | 未対応。sceneの公開ActionPostにはこれらがなく、scene設定の欠落とは断定しない |
| grouped_light | on、dimming、dimming_delta、color_temperature、color_temperature_delta、color、alert、signaling、dynamics | グループの直接制御は未対応。room/zone構成の管理とは別 |
| device | identify.action | CLIのidentifyで対応 |
| device | usertest.usertest | 一時テストモード。未対応 |
| device_software_update | install.install_state | 更新開始操作。未対応。自動更新の永続設定とは別 |
| entertainment_configuration | action（start / stop） | ストリーミングの開始・停止。未対応 |
| geofence_client | is_at_home | 在宅状態の報告。未対応 |
| homekit / matter | action（homekit_reset / matter_reset） | 連携のリセット。未対応。ペアリング設定全体の仕様は未確定 |
| zigbee_device_discovery | action.action_type（search） | 探索開始。未対応。機器登録はアプリ担当の既存方針 |
| device | DELETE | 機器登録解除は提供しない。hue_deviceのdestroyはTerraform管理の解除のみ |

この表の項目は固定コミットの各 `<resource>/schemas/*Put.yaml` とdeviceのDELETEルートを根拠とする。
CLIのrecallは前表のとおり一部対応。API v1の旧ルールは調査対象外。

## 読み取り側の不足

現在のdata sourceはlight / deviceのUUID指定のみ。取得属性もAPI応答全体ではない。

- light: 名前、owner device ID、色・色温度対応、色域種別、mirek範囲を公開。現在の点灯・明るさ・xy・mirek、effect状態、powerup、詳しい色域座標等は公開しない。
- device: 名前、型番、light ID集合を公開。製品情報全体や照明以外のサービス一覧は公開しない。resourceではname / archetype / light_idsを公開。
- scene: 設定は読めるが、現在の `status.active` はTerraform属性にない。smart_scene.state、behavior.status / last_errorは対応済み。
- device_power、zigbee_connectivity / zgp_connectivity / wifi_connectivity、device_software_update: 電池・接続・更新状態の専用data sourceなし。
- motion / light_level / temperature / contact / camera_motion、button / relative_rotary / bell_button / tamper等: 検知値・イベント・状態の専用data sourceなし。
- behavior_script: 設定schema・script一覧のdata sourceなし。CLIの一覧・raw取得とは区別する。
- 部屋・ゾーン等の一覧検索、entertainment、Bridgeや連携状態の専用data sourceなし。
- eventstreamのイベント購読なし。Terraformのrefreshとは別用途。

## 公開資料だけでは書込機能を確定できない範囲

以下のPUTスキーマには `type` 以外の項目がない。ルートの存在だけで設定機能を数えない。

`behavior_script`、`bell_button`、`bridge`、`entertainment`、`geolocation`、
`grouped_light_level`、`grouped_motion`、`matter_fabric`、`motion_area_candidate`、
`relative_rotary`、`speaker`、`tamper`、`wifi_connectivity`、`zgp_connectivity`。

Bridge名・時刻設定・位置情報、スクリプトのアップロード、エリア作成、カメラ全体の設定、
アプリの並び順・お気に入りなどは「API対応が確定した未実装設定」の数に含めない。
`bridge_home`、`device_power`、全体resource取得、eventstreamは、この資料ではGETのみ。
認証登録のPOSTはhue-tf initで扱い、Terraform resourceにはしない。

## 公開前と公開後の判断

- **公開前の優先調査**: 既に管理するsceneのdynamics / appdata、smart_sceneのappdataが更新時に失われないか。対応・保持・明示的な拒否のいずれかを決める。
- **公開時に制限を明記**: smart_scene作成時のimage指定、recallの追加パラメーター、JSON属性のvalidation範囲。
- **公開後の追加候補**: powerup、センサー設定、情報取得、エンターテインメント構成。未対応であること自体を公開阻害条件としない。
- **別途スコープ判断**: 現在点灯状態の直接制御、ネットワーク変更、連携リセット、機器探索、イベント購読。

優先順位は提案であり、この一覧は実装の承認・完全な公式API準拠の宣言ではない。
