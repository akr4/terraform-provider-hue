# 設定機能の対応表

確認日: 2026-09-17。利用者が保存した公式Hue API v2リファレンスと、プロバイダーの実装を照合した。
APIの項目別の不足、根拠リンク、公開前後の区分は[API対応範囲](api-coverage.md)を参照する。
以前の非公式スキーマによる判定は、公式資料で確認できた内容に置き換えている。

「対応」は設定を表現・送信できる意味で、全機種の動作保証ではない。「JSON対応」は内部項目の専用validationがある意味ではない。
未対応、公式資料で確認できない項目、実行時操作としてTerraform設定と分離する項目を区別する。

## 照明・機器

| 利用者が行う設定・確認 | 状況 | 境界 |
|---|---|---|
| 機器の名前・アイコン変更 | 対応 | hue_device.name / archetype。照明の名前変更とアプリ反映は利用者確認済み |
| 機器から照明を参照する | 対応 | hue_device.light_ids。data sourceでも取得可能。機器IDとlight IDは別 |
| 部屋への機器所属・ゾーンへの照明所属 | 対応 | room.childrenはdevice ID、zone.childrenはlight ID |
| 電源復帰時の点灯・明るさ・色 | 未対応 | light.powerup。公式のcustom構造を確認済み、実機書込は未検証 |
| 最小調光レベル | 未対応 | light.dimming_configuration.min_level |
| 照明サービスの用途 | 未対応 | light.metadata.function。機器の名前管理とは別 |
| 配置・向き・ピクセル位置・画面との関連 | 未対応 | device / roomのgeometry、lightのgeometry / content_configuration / association |
| 識別点滅 | CLI対応 | identify。APIのduration指定は未対応 |
| ペアリング・探索・登録解除 | 対象外 | アプリ担当。hue_deviceのdestroyは管理解除だけで機器と設定を残す |
| ファームウェア更新開始 | 要確認 | GETの状態取得とPUTの操作は別。保存した公式PUTではinstall項目を確認できない |

## センサー・スイッチ

| 利用者が行う設定・確認 | 状況 | 境界 |
|---|---|---|
| 検知の有効無効・感度 | 未対応 | motion、camera_motion、convenience_area_motion、security_area_motion |
| 照度・温度測定の有効無効 | 未対応 | light_level / temperature.enabled |
| 集約サービス・接触センサーの有効無効 | 未対応 | grouped_motion / grouped_light_level / contact.enabled |
| 昼光感度、時間帯別シーン、対象部屋、待ち時間、消灯動作 | JSON対応 | behavior.configuration。script固有設定。センサー自体の感度とは別 |
| 短押し・長押し・巡回・ダイヤルの割り当て | JSON対応 | behavior_instance。機種・scriptごとのvalidationは主にBridgeが担当 |
| 割り当ての名前・有効無効・削除 | 対応 | behaviorのname / enabled / Delete。物理機器の名前とは別 |
| 壁スイッチ入力モード | 未対応 | switch_input_configuration.switch_mode。旧device_modeは非推奨 |
| 電源出力モード | 未対応 | power_output_configuration.output_mode |
| ボタンのrepeat間隔・control_id設定 | 要確認 | 非公式スキーマにはあるが、保存した公式PUTでは確認できない |
| 電池・接続状態・現在の照度・温度・検知状態 | 未対応 | 専用data sourceなし。CLIのraw取得とは区別する |

## シーン・自動化

| 利用者が行う設定・確認 | 状況 | 境界 |
|---|---|---|
| 部屋・ゾーン・シーンの作成、名前、削除 | 対応 | room / zone / scene。sceneのgroup変更は置換 |
| シーン内の点灯・明るさ・xy色・色温度 | 対応・範囲制限あり | mirek / kelvinはproviderの153〜500制限が公式型の50〜1000より狭い |
| 配色原本・ダイナミック速度・自動プレイ | 対応 | paletteはJSON。colorは公式上最大9要素でありgradient.pointsの最大5とは別 |
| gradient・従来effects | JSON対応 | gradientの内部設定を保持可能。従来effectsは非推奨 |
| 新しいeffects_v2 | JSON対応 | palette・scene.actionsともに対応。action / parametersを保持 |
| シーン内の遷移時間 | JSON対応 | actions[].action.dynamics.duration（ミリ秒）。省略時は読取値を保持 |
| シーンの空間マッピング | 未対応 | mapping.algorithm。SpatialAware対応機種向け |
| アプリ固有の付加情報 | 未対応 | scene / smart_sceneのmetadata.appdata。省略更新時の保持を要確認 |
| シーンの画像 | 管理対象外 | 通常・スマートシーンとも画像を送信しない。旧stateのimage_idはローカル移行で除去 |
| スマートシーンの曜日・時刻・日没・遷移時間 | 対応 | hue_smart_scene。画像は管理対象外 |
| 再生・ダイナミック開始・スマートシーン稼働切替 | CLI対応 | recall.action。再生時のduration / dimming指定は未対応 |
| 自動化の構成・名前・有効無効・削除 | 対応 | behavior_instance。configurationはJSON全体を管理 |
| 自動化の明示的な実行要求 | 未対応 | behavior.trigger。configurationとは別 |
| 独自formulaの登録・削除 | 未対応 | behavior_script_formulaの公式POST/DELETEあり。HSLと各schemaを扱う |

## その他

| 項目 | 状況 | 境界 |
|---|---|---|
| エンターテインメントエリアの作成・照明選択・位置・明るさ補正・proxy | 未対応 | 公式のPOST/PUTで設定項目を確認。実際のストリーミングとは別 |
| MotionAware関連エリアの構成・感度 | 未対応 | motion_area_configurationの名前・group・participants等、motion系サービスの感度 |
| 位置情報・Zigbeeチャンネル | 未対応 | geolocationの緯度経度、zigbee_connectivity.channel |
| Bridgeの時刻・自動更新・アプリの並び順等 | 要確認 | サンプルやGETにあるだけで書込可能とは判断しない |
| スピーカーの音再生・消音、HomeKit/Matterリセット | 未対応 | 実行時操作として別スコープ |
| v1旧ルール | 対象外 | v2のみの既存方針。behavior作成のmigrated_fromも未対応 |
| 既存.tfの自動同期 | 対象外 | Terraformのrefresh/planと手動編集を基本とする。import-blocksは新規importブロック生成だけ |

## 公開前の優先事項

1. scene.actionsのeffects_v2 / dynamicsは保持に対応済み。他の未対応属性の更新時保持を確認する。
2. mirek / kelvinの固定範囲を整理する。画像は管理対象から除外済み。
3. native import/config生成時のmirek / kelvin重複、実機で判明したactions対象一致を回帰テストへ反映する。
4. 管理範囲とdestroyの意味を明記し、CI・配布バイナリ・通常インストールの経路を確認する。

powerupやセンサー設定などの新機能は公開後の候補。未対応であること自体を公開阻害条件にしない。
API項目の存在、機種での利用可否、プロバイダーとして提供する範囲は別々に判断する。
