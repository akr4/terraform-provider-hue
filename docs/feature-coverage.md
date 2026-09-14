# 設定機能の対応表

確認日: 2026-09-14。アプリで行う操作を起点に、プロバイダーの schema・送受信コード、公開資料、手元 Bridge の読み取り結果を照合する。機能追加時はこの表も更新する。

本表は Bridge 接続による照明・アクセサリー管理を主対象とする。全機種・全アプリ画面の網羅を保証しない。アプリの画面・機種・ファームウェアによって異なる項目は「要確認」とする。未対応とAPI非提供を同一視しない。

## 判定と根拠

- **対応**: 専用のリソース属性で設定できる。全機種での実機検証を意味しない。
- **JSON対応**: JSON全体を管理できる。スクリプト固有の形や意味は主にBridgeが検証する。
- **読取のみ**: data sourceまたはComputed属性で取得する。
- **未対応**: このプロバイダーの設定・data sourceでは扱わない。補助CLIのraw取得とは区別する。
- **対象外**: 既存の設計上、アプリまたは明示的な実行時操作に任せる。
- **要確認**: アプリ項目と公開APIの対応、または提供範囲をまだ確定できない。対象外と決めたものではない。

Hue公式のAPI v2詳細リファレンスは今回アクセスできなかった。API項目は主に[OpenHueの公開スキーマ](https://github.com/openhue/openhue-api/tree/1ffc817857abf456d5ff2ae50400ef768dbce28e/src)で照合した。これは非公式資料であり、実機仕様の最終的な保証ではない。手元Bridgeではdeviceのmetadata、lightのpowerup、motionのenabled/sensitivity、電池・接続・エンターテインメント関連リソースの存在をGETで確認した。書き込みによる網羅試験は行っていない。

## 照明・スイッチ・センサー本体

| 利用者が行う設定・確認 | 対応状況 | API・実装上の根拠と境界 |
|---|---|---|
| 機器の名前変更（照明・スイッチ・センサー） | **対応（device metadata）** | resource hue_device.nameで変更可能。deviceとlight両方にmetadataがあるため、light名・アプリ表示への伝播は実機未確認。[D] |
| 機器の種類・アイコン変更 | **対応（device metadata）** | resource hue_device.archetype。部屋のarchetypeとは別。機種別の対応値・アプリ表示は実機未確認。[D] |
| 部屋への機器の所属・移動 | **対応** | hue_room.childrenはdevice ID。移動は移動元・先両方の構成を編集。複数部屋への同時所属を解決する独自処理はない。 |
| ゾーンへの照明の所属 | **対応** | hue_zone.childrenはlight ID。 |
| 電源復帰時の挙動、復帰時の明るさ・色 | **未対応** | light.powerup。プリセットとcustom設定がある。通常シーンの保存・recallとは別。[L] |
| 機器の識別点滅 | **対象外（CLI対応）** | hue-tf identify。Terraformの永続設定ではない。 |
| ペアリング・新しい機器の探索 | **対象外** | 登録はアプリで行う既存方針。resource追加で物理機器を新規作成する意味にしない。 |
| 機器の登録解除・工場リセット | **対象外** | 現在はアプリ側の機器管理。hue_deviceのDeleteはTerraformの管理だけを解除し、機器と設定を残す。 |
| ファームウェアの確認、更新操作 | **未対応／要確認** | device_software_updateは存在する。公開Putのinstallは一回限りの操作。アプリの自動更新設定との対応は未確認。[U] |

[D]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/device/schemas/DevicePut.yaml
[L]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/light/schemas/LightPut.yaml
[U]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/device_software_update/schemas/DeviceSoftwareUpdatePut.yaml

アプリには電源復帰時のカスタム色や前回状態への復帰がある。[公式リリースノート](https://www.philips-hue.com/pt-pt/support/release-notes/android)。APIのpowerup詳細スキーマには構造上の疑問もあるため、各custom属性は実装時に公式仕様または実機で追加確認する。

## センサー・スイッチの動作

| 利用者が行う設定・確認 | 対応状況 | API・実装上の根拠と境界 |
|---|---|---|
| モーション検知そのものの有効・無効 | **未対応** | motion.enabled。behavior.enabledとは別。[M] |
| 動きを検知する感度 | **未対応** | motion.sensitivity.sensitivity。上限は機器側のsensitivity_max。[M] |
| 昼光感度（明るいときに点灯させない閾値） | **JSON対応** | センサー用behavior.configurationのdaylight_sensitivity。実際の管理構成で確認。light_level.enabledと混同しない。 |
| 時間帯別のシーン、対象部屋、点灯後の待ち時間、消灯動作 | **JSON対応** | hue_behavior_instance.configuration。利用するscriptの対応範囲に依存する。 |
| 「邪魔しない」等、点灯中の照明への動作 | **JSON対応／要確認** | 設定JSONは保持できる。アプリの各選択肢に対応するキーと意味はscriptごとに照合が必要。 |
| スイッチの短押し・長押し・巡回・ダイヤルの割り当て | **JSON対応** | behavior_instance。すべての機種・scriptを検証したという意味ではない。 |
| 動作の名前変更・有効無効・割り当て削除 | **対応** | behaviorのname/enabled/Delete。物理スイッチの名前やペアリングには影響しない。 |
| ボタンイベントの繰り返し間隔 | **未対応／要確認** | button.button.repeat_intervalが公開スキーマにある。アプリの長押し動作との関係・機種対応は未確認。[B] |
| 照度・温度測定サービスの有効無効 | **未対応／要確認** | 実機にlight_level.enabled、temperature.enabledがある。アプリ独立項目の有無と書込仕様は別途確認。 |
| 電池残量、接続状況、現在の照度・温度・検知状態 | **未対応（専用data sourceなし）** | device_power、zigbee_connectivity、light_level、temperature、motionは実機で確認。raw取得は可能だがTerraformの読取属性は未提供。 |

[M]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/motion/schemas/MotionPut.yaml
[B]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/button/schemas/ButtonPut.yaml

昼光感度とモーション感度、時間帯別動作と待ち時間はアプリ側でも別の設定。[公式モーションセンサーガイド](https://www.philips-hue.com/en-us/explore-hue/blog/motion-detection-lighting)。画面上でセンサー全体を無効化する操作がmotion.enabledとbehavior.enabledのどちらを変更するかは、実装時に切り分ける。

## 部屋・シーン・自動化

| 利用者が行う設定・確認 | 対応状況 | API・実装上の根拠と境界 |
|---|---|---|
| 部屋・ゾーンの作成、名前、アイコン、削除 | **対応** | hue_room/hue_zone。 |
| シーンの作成、名前、削除 | **対応** | hue_scene。所属group変更は置換。 |
| シーン内の点灯・消灯、明るさ、xy色、色温度 | **対応** | actions。色温度はmirek/kelvin。部屋の全照明を含める必要があることを実機で確認。 |
| シーンの配色原本、ダイナミック速度・自動プレイ | **対応** | paletteはJSON、speed/auto_dynamicは専用属性。actionsとは独立。 |
| グラデーション・従来effects | **JSON対応** | actions.gradient/effects。詳細はBridgeが検証。 |
| シーンactionsのdynamics（遷移時間等） | **未対応** | 公開ActionPostにdynamicsがあるが、現在のAction型は保持しない。[A] |
| 新しいeffects_v2・timed_effects等 | **未対応／要確認** | light APIにはあるが、scene actionsでの書込・保持仕様は別途確認。任意項目をすべて往復保持する実装ではない。[L] |
| シーン画像 | **一部対応** | 通常シーンのimage_id参照は設定可。画像のアップロード・ギャラリー検索はない。変更不可の既存画像を再送しない対処あり。 |
| 曜日・時刻・日没によるシーン切り替え | **対応** | hue_smart_scene。公開timeslotのtime/sunsetに対応。未確認のsunrise対応を欠落と扱わない。[S] |
| スマートシーンの画像変更 | **読取のみ** | image_idはComputed。 |
| シーン再生、ダイナミック開始、スマートシーンの稼働切替 | **対象外（CLI対応）** | hue-tf recall。自動プレイの保存設定とは区別する。 |
| 自動化の名前・有効無効・削除 | **対応** | behavior_instanceに対応する自動化。すべてのアプリ自動化が同形式とは保証しない。 |
| 自動化の曜日・時刻・対象・動作 | **JSON対応** | Bridge上のbehavior scriptが受理するconfiguration全体。独自scriptのアップロードは非対応。 |
| ホーム画面の表示順・お気に入り・シーンの整理順 | **要確認** | 対応属性なし。アプリ内設定・クラウド設定・Bridge設定のどこにあるか未確定。 |

[A]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/scene/schemas/ActionPost.yaml
[S]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/smart_scene/schemas/SmartSceneTimeslotGet.yaml

## その他の領域と方針

| 項目 | 対応状況 | 境界 |
|---|---|---|
| エンターテインメントエリアの名前・構成・照明位置 | **未対応** | 実機にentertainment_configurationとlocationsが存在。部屋/ゾーンとは別リソース。名前等の公開Putは確認したが、位置の編集・作成仕様は追加確認が必要。[E] |
| 映像・音楽とのリアルタイム同期 | **対象外** | 永続構成の管理と区別する。エリアの定義まで対象外と決定したものではない。 |
| Bridgeの名前、位置、タイムゾーン、ネットワーク・Zigbee設定 | **未対応／要確認** | host/keyは接続設定でありBridge本体の設定ではない。Zigbee channelの公開Putはある。他の項目のv2書込可否は未確定。[Z] |
| 複数Bridge | **未検証** | provider aliasによる個別構成は設計上可能。横断リソースや自動移行は実装していない。 |
| Secure・カメラ・接触センサー・MotionAware等 | **未対応／要確認** | 本監査では公開API・機種ごとの項目まで照合していない。behaviorで扱える一部設定と領域全体への対応を混同しない。 |
| Matter・HomeKit・外部アカウント連携 | **未対応／要確認** | ライフサイクルと認証を含む別領域。現在の対象範囲から自動的に拡張しない。 |
| v1の旧ルール | **対象外** | v2のみ対応する既存方針。v2に見えない参照が存在しないと断言しない。 |
| アプリで変更した既存設定の.tfへの自動同期 | **対象外** | Terraformのrefresh/planと手動編集を基本とする。import-blocksは未管理分のimportブロック作成のみ。 |

[E]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/entertainment_configuration/schemas/EntertainmentConfigurationPut.yaml
[Z]: https://github.com/openhue/openhue-api/blob/1ffc817857abf456d5ff2ae50400ef768dbce28e/src/zigbee_connectivity/schemas/ZigbeeConnectivityPut.yaml

アプリにエンターテインメントエリアと同期の設定があることは[公式の移行ガイド](https://www.philips-hue.com/ja-jp/support/article/how-to-upgrade-to-the-hue-bridge-pro/000010)で確認。自動更新設定は[公式Bridgeリリースノート](https://www.philips-hue.com/ja-jp/support/release-notes/bridge)にあるが、公開v2のinstall操作と同じ設定ではない。

## 基本情報の取得

現在のdata sourceはhue_light/hue_deviceのUUID指定のみ。lightは名前・device ID・色/色温度対応・色域種別・mirek範囲、deviceは名前・型番・light ID集合を返す。部屋・ゾーン内の照明一覧、behavior scriptの構成スキーマ、電池や接続状況のdata sourceはない。

## 実装の優先順位案

1. **機器の名前・archetypeの実機確認**。hue_deviceを実装済み。import、未指定属性の維持、削除・対象変更で登録解除しない動作は模擬Bridgeでテスト済み。deviceとlightの名前・アプリ表示の関係は実機確認が残る。
2. **電源復帰時設定**。機種の能力に応じたvalidationとcustom設定の組合せを扱う。
3. **motionの有効無効・検知感度**。behavior設定と分離し、機器の感度上限を取得する。
4. **情報取得の拡充**。部屋のライト一覧・電池・接続状態を必要なユースケースから追加する。
5. **シーン設定の保持範囲を拡充**。dynamics等の未対応項目が更新で失われるかを検証し、対応または明示的な拒否方針を決める。

順序は提案であり、機能追加の承認を意味しない。

## 信頼性・保守上の別課題

- 実機で確認したactions対象一致の制約をfake Bridgeと回帰テストへ反映する。フェイクテスト成功だけでは実機の受理を保証しない。
- paletteやbehaviorのJSON検証はオブジェクト形式が中心。サイズ・項目範囲・機種固有制約までの事前検証は限定的。
- native import/config生成で相互排他のmirek/kelvinが同時に出るケースがある。color_hex削除だけで解消したと扱わない。
- Scene v0→v1のstate移行テストはあるが、hue_deviceのimport・削除時の機器保持は模擬Bridgeでテスト済み。将来のschema変更には移行テストが必要。
- CIのfake acceptance、ドキュメント生成、GoReleaser/GPG設定は存在する。Registry公開・実際のリリース成功・現時点のCI状態は本監査では未確認。
- 古いpull説明と現行import-blocksの役割を混在させない。

## コードの参照先

- [リソース登録](../internal/provider/provider.go)
- [部屋・ゾーン](../internal/provider/group_resource.go)
- [シーン](../internal/provider/scene_resource.go)、[palette](../internal/provider/scene_palette.go)、[受送信型](../internal/hue/types.go)
- [behavior](../internal/provider/behavior_resource.go)
- [スマートシーン](../internal/provider/smart_scene_resource.go)
- [data source](../internal/provider/data_source.go)
- [CI](../.github/workflows/test.yml)、[リリース](../.github/workflows/release.yml)
