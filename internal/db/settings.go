package db

import (
	"strconv"

	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

func GetSetting(key string) string {
	var s models.Setting
	if err := DB.Where("key = ?", key).First(&s).Error; err != nil {
		return ""
	}
	return s.Value
}

func GetSettingInt(key string, def int) int {
	v := GetSetting(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func GetSettingBool(key string, def bool) bool {
	v := GetSetting(key)
	if v == "" {
		return def
	}
	return v == "true" || v == "1"
}

func SetSetting(key, value string) error {
	return DB.Save(&models.Setting{Key: key, Value: value}).Error
}

func AllSettings() map[string]string {
	var rows []models.Setting
	DB.Find(&rows)
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	return m
}
