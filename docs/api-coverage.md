# Hue API v2とプロバイダーの対応範囲

確認日: 2026-09-17。公開前の修正と公開後の機能拡張を判断するための一覧。
アプリ操作から見た概要は[設定機能の対応表](feature-coverage.md)を参照する。

## 根拠と範囲

利用者が保存した[公式Hue CLIP API v2 Reference][official]のHTMLを基準に、プロバイダーのschema、受信型、送信payload、補助CLIを照合した。
保存資料はraml2html 7.8.0で生成されたもの。154操作のうちPOST/PUT/DELETEは64操作、PUTを持つリソース種別は41種類。
コレクションと個別リソースの操作を区別し、POST固有の設定も確認した。
元HTMLと検索用の抽出JSONはローカル資料として扱い、このリポジトリには収録しない。
本書は実装との差について独自に整理したもので、公式仕様書や機械可読schemaの再配布ではない。

以前の[OpenHueスキーマ](https://github.com/openhue/openhue-api/tree/1ffc817857abf456d5ff2ae50400ef768dbce28e/src)を基にした一覧を訂正した。
例えば公式仕様ではsceneのeffects_v2やroomのgeometryが存在し、service_groupはservicesではなくchildrenを使う。
逆にbutton.repeat_intervalやdevice_software_update.installは保存した公式PUTのプロパティとして確認できなかった。

**記載と動作確認は別:** この資料で全機種の動作、既存値の保持、API操作の副作用まで検証したわけではない。
本文のプロパティを根拠とし、サンプルJSONだけにある項目を設定可能とは判断しない。
資料にない項目も「全バージョンでAPI非対応」とは断定しない。

- **専用属性**: Terraformの型付き属性で管理する。
- **JSON対応**: JSON全体を保持・送信できる。各項目のvalidationや実機検証があるという意味ではない。
- **未対応**: 設定・取得属性として公開されていない。
- **CLIのみ**: 一回限りの操作として補助CLIで提供する。
- **仕様差あり**: 公式資料の構造・範囲と現在の実装が一致しない。

永続設定、現在状態を変える操作、情報取得は区別する。未対応一覧は、すべてをTerraform resourceへ追加する計画ではない。

[official]: https://developers.meethue.com/develop/hue-api-v2/api-reference/

## 対応済みリソースに残る不足・仕様差

| 対象 | 対応済み | 未対応・仕様差 |
|---|---|---|
| device | metadata.name / archetype、light_idsの取得 | geometry.objects（サービスの位置・回転）、旧device_mode。旧modeは非推奨でswitch_input_configurationが後継。[Device][device] |
| room | children、metadata.name / archetype | geometry.objectsの位置・回転。[Room][room] |
| zone | children、metadata.name / archetype | 保存資料のPOST/PUT設定項目に確認できた欠落なし。roomのgeometryをzoneにもあると扱わない。[Zone][zone] |
| scene.actions | on、brightness、xy、mirek/kelvin、gradient / effects / effects_v2 / dynamicsのJSON | effects_v2のaction / parameters、dynamics.durationを保持・設定可能。旧effectsは公式では非推奨。[Scene PUT][scene-put] |
| scene本体 | name、group、palette、speed、auto_dynamic | **metadata.appdata**、**mapping.algorithm**（SpatialAware対応機種のclassic / spatial）が未対応。[Scene POST][scene-post] |
| scene / smart_sceneの画像 | 管理対象外 | 作成・更新ともmetadata.imageを送らない。公式POSTには画像参照があるが、画像の登録APIは保存資料にない。旧stateのimage_idはローカル移行で除去する |
| sceneの色温度 | mirek / kelvin指定、機器の能力範囲の取得 | 入力検証・kelvin変換は公式型の50〜1000に対応済み。照明のmirek_schemaを用いて機種側の補正と比較する。能力範囲が不明な場合はAPI範囲を使う |
| smart_scene | name、group、week_timeslots、transition_duration、稼働状態の読取 | metadata.appdataが未対応。画像は管理対象外。[Smart scene][smart-post] |
| behavior_instance | script_id、name、enabled、configuration全体、status / last_errorの読取 | POSTのmigrated_from（v1由来ID）とPUTのtriggerが未対応。前者は移行補助、後者は実行時操作。[Behavior POST][behavior-post]、[PUT][behavior-put] |

paletteとactionsのeffects_v2は区別する。**palette.effects_v2、actionsのeffects_v2ともにJSONで保持・設定できる。**
palette.colorは公式資料では最大9要素、dimming / color_temperatureは各最大1、effects / effects_v2は各最大3。
gradient.pointsは最大5要素で、palette.colorの上限とは別。providerはこれらの配列長・内部構造を網羅的には検証しない。

**更新時の保持:** scene更新はactions全体を組み直して送る。effects_v2 / dynamicsは、設定を省略した場合も読取値を保持して再送する。
fakebridgeでimport・更新・省略時保持を検証済み。全機種での実機動作は未検証。

appdata、mapping、device / roomのgeometryは管理対象外とし、更新リクエストに含めない。
scene / smart_sceneのmetadata.appdata、sceneのmapping、device / roomのgeometryが、nullも含めてPUTに現れないことをTerraform経由の回帰テストで検証する。
取得した既存値の再送もしない。metadataは管理対象のname等だけを含むため、metadata内の未指定項目を維持する部分更新がBridge側で必要となる。
このテストは送信内容の保証であり、実機での保持を証明するものではない。
sceneのmetadata.appdataについては、実機の既存シーンをTerraformで名前変更し、更新前後の値が一致することを確認済み。
同じ確認で、actionsは配列順序のみが変わり、各ターゲットの設定は一致した。
smart_sceneのappdata、sceneのmapping、device / roomのgeometryの実機での保持は未検証。sceneでの結果を他リソースへ一般化しない。

[device]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_device__id__put
[room]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_room__id__put
[zone]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_zone__id__put
[scene-put]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_scene__id__put
[scene-post]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_scene_post
[smart-post]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_smart_scene_post
[behavior-post]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_behavior_instance_post
[behavior-put]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_behavior_instance__id__put

## APIに設定項目があるが、専用管理機能がないもの

| APIリソース | 未対応の設定項目 | 用途・境界 |
|---|---|---|
| light | metadata.name / function、旧metadata.archetype | service側の名前・用途。deviceの名前管理とは別。service側archetypeは非推奨。[Light][light] |
| light | dimming_configuration.min_level | 最小調光レベルの設定 |
| light | powerup.preset、on / dimming / color | 電源復帰時の点灯・明るさ・色。on / dimming / colorはpowerup直下。非公式スキーマにあった入れ子の疑問は公式HTMLで解消 |
| light | content_configuration.orientation / order、association.association、geometry.pixel_positions | ピクセル照明の向き・順序、画面との関連、ピクセルの空間位置 |
| motion | enabled、sensitivity.sensitivity | モーション検知の有効無効・感度。behavior.enabledや昼光感度とは別。[Motion][motion] |
| light_level / temperature | enabled | 照度・温度測定の有効無効 |
| grouped_motion / grouped_light_level | enabled | 集約サービス側の有効無効。元の個別サービスとは別 |
| camera_motion / convenience_area_motion / security_area_motion | enabled、sensitivity.sensitivity | 個別カメラ・エリア単位の検知設定 |
| contact | enabled | 接触センサーの有効無効 |
| switch_input_configuration | switch_mode.mode | 壁スイッチ入力の単／二連・ロッカー／押しボタン等のモード。[Switch input][switch-input] |
| power_output_configuration | output_mode.mode | controllable / always_onの出力モード。[Power output][power-output] |
| service_group | children、metadata.name | サービスのグループ。POST/PUT/DELETEあり。room / zoneとは別。[Service group][service-group] |
| geolocation | latitude、longitude | 位置情報。両項目がPUT本文に定義されている。[Geolocation][geolocation] |
| geofence_client | name | クライアント作成・名前の設定・削除。is_at_homeは現在状態として次表に分離 |
| entertainment_configuration | metadata.name、configuration_type、stream_proxy、locations.service_locations | 作成・更新・削除。サービス選択、位置、equalization_factorも設定可能。映像・音楽の送信とは別。[Entertainment][entertainment-config] |
| motion_area_configuration | name、group、participants[].resource、enabled | モーションエリアの作成・更新・削除。感度は別のmotion系サービス。[Motion area][motion-area] |
| behavior_script_formula | description、metadata、configuration_schema、trigger_schema、state_schema、version、supported_features、language / content | HSL formulaの作成・削除。公開済みscriptを使うbehavior_instanceの管理とは別。[Formula][formula] |
| zigbee_connectivity | channel.value | Zigbeeチャンネル。not_configuredもenumにあるが、実機での設定可否を未確認 |

[light]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_light__id__put
[motion]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_motion__id__put
[switch-input]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_switch_input_configuration__id__put
[power-output]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_power_output_configuration__id__put
[service-group]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_service_group_post
[geolocation]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_geolocation__id__put
[entertainment-config]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_entertainment_configuration__id__put
[motion-area]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_motion_area_configuration_post
[formula]: https://developers.meethue.com/develop/hue-api-v2/api-reference/#resource_behavior_script_formula_post

## 実行時操作・現在状態の変更

APIとの差ではあるが、すべてを永続設定resourceに載せるとは限らない。

| 対象 | 項目・操作 | 現在の対応 |
|---|---|---|
| scene / smart_scene | recall.action | CLIで対応。sceneのrecall.duration / dimmingはCLIでも未対応 |
| light | on、dimming、color、color_temperature、gradient、effects / effects_v2 | 直接操作は未対応。sceneへの保存は上表の範囲で対応 |
| light | dimming_delta、color_temperature_delta、dynamics、alert、signaling、timed_effects | 直接操作は未対応。timed_effectsをsceneの保存項目としては確認できない |
| grouped_light | on、dimming、dimming_delta、color_temperature、color_temperature_delta、color、alert、signaling、dynamics | グループの直接操作は未対応 |
| device / light | identify.action / duration | CLIはlight IDも受け付けるがowner deviceへ送る。duration指定は未対応。CLIの回数指定とは別 |
| device | usertest.usertest | 一時テストモード。未対応 |
| behavior_instance | trigger | scriptのtrigger_schemaに従う実行要求。configuration JSONの管理では代替しない |
| entertainment_configuration | action | ストリーミングの開始・停止。未対応 |
| geofence_client | is_at_home | 在宅状態の報告。未対応 |
| speaker | alarm / chime / alertのsound・volume、alarm.duration、mute.mute | 音の再生・消音。未対応 |
| homekit / matter | action | 連携リセット。未対応。公式のmatter.actionは文字列であり、非公式スキーマのaction.action_typeとは異なる |
| zigbee_device_discovery | action.action_type / search_codes / search_channels、add_install_codes | 機器探索・install code登録。未対応 |
| device / matter_fabric | DELETE | 機器登録解除・fabric削除は未対応。hue_deviceのdestroyは管理の解除だけ |

## 情報取得の不足

data sourceはlight / deviceのUUID指定のみで、API応答全体を返すものではない。

- light: 名前、device ID、色・色温度対応、色域種別、mirek範囲を公開。現在の点灯・明るさ・xy・mirek、effectsの状態、powerup、geometry等は公開しない。
- device: 名前、型番、light ID集合を公開。製品情報全体や他サービスのID、geometryは公開しない。resourceはname / archetype / light_idsを公開する。
- scene: status.activeを公開しない。smart_sceneの稼働状態、behaviorのstatus / last_errorは対応済みだが、behaviorのscript固有stateは公開しない。
- device_power、zigbee_connectivity / zgp_connectivity / wifi_connectivity、device_software_update: 電池・接続・更新状態の専用data sourceなし。
- motion / light_level / temperature / contact / camera_motion、button / bell_button / relative_rotary / tamper等: 検知値・イベント・状態の専用data sourceなし。
- behavior_script / behavior_script_formulaの定義・schema、entertainment、Bridge・連携状態の専用data sourceなし。
- リソースの一覧検索、部屋内の照明一覧のdata sourceなし。CLIのls / rawとは別。
- イベント購読は未実装。今回のHTMLはresourceリファレンスであり、eventstreamの詳細仕様までは今回の照合に含めない。

## 設定可能と確認できなかった項目

次のPUTにはid / type以外の本文プロパティを確認できなかった。
`bridge`、`device_software_update`、`device_power`、`zgp_connectivity`、`button`、`bell_button`、
`relative_rotary`、`entertainment`、`tamper`、`motion_area_candidate`、`clip`、`wifi_connectivity`。

- buttonのcontrol_id / repeat_interval、device_software_updateのinstallは、前の非公式スキーマだけでは対応可否を確定しない。
- bridgeの例にはtime_zoneがあるが、PUTのプロパティ定義にはない。書込対応の根拠にはしない。
- light.modeはGET側の状態として扱い、今回の公式PUTで設定可能とは確認できない。
- behavior_script、matter_fabric、bridge_homeにはこのHTMLでPUTを確認できない。formulaは別リソースでPOSTあり、matter_fabricはDELETEあり。
- Bridgeの自動更新、ホーム画面の表示順・お気に入り等は、この資料から書込項目との対応を確定できない。
- v1旧ルールとクラウドAPIは対象外。hue-tf initの認証登録も、このv2 resource一覧とは別。

## 公開前と公開後の判断

**公開前に優先して確認・修正する項目**

1. scene.actionsのeffects_v2 / dynamicsの保持はJSON属性で対応済み。機種固有のパラメーターや実機応答の差を確認する。
2. 画像は管理対象外とし、POST/PUTに含めない（対応済み）。旧state移行と画像を持つシーンの更新を回帰テストで確認する。
3. mirek / kelvinの範囲は50〜1000に修正済み。fakebridgeで拡張範囲の作成・更新・importと従来の機種範囲補正を検証する。
4. appdata、mapping、device / room geometryの更新時保持。省略で残るものまで、公開前の機能追加を必須にしない。
5. 標準import/config生成、JSON属性の制約、非対応項目を含むリソースの扱いを説明する。

**公開後の機能追加候補**

powerup、最小調光レベル、センサー設定、switch_input_configuration / power_output_configuration、情報取得、
位置・配置情報、エンターテインメント構成、formula、MotionAware関連。APIがあることと製品として提供する判断は分ける。

**補助CLI等の別スコープ候補**

recall / identifyの追加パラメーター、behavior.trigger、直接制御、音再生、探索、ネットワーク・連携操作。

これは実装範囲の提案であり、本調査ではproviderコードや実機設定を変更していない。
