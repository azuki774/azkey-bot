package domain

import "time"

// チェックインの設定。変更後にビルド・再デプロイすると反映されます。

// CheckInDayStart は JST での日付の区切り時刻です（0以上24時間未満）。
// 例: 午前4時30分なら 4*time.Hour + 30*time.Minute。
// 記録の加算と連続日数の照会に共通で使います。
const CheckInDayStart = 5 * time.Hour

// DefaultCheckInKeywords は投稿本文に部分一致で反応するワードです。
// ワードの追加・削除・変更はこの一覧で行います。空文字列は指定できません。
func DefaultCheckInKeywords() []string {
	return []string{
		"ログボ",
		"ログインボーナス",
		"出勤",
	}
}
