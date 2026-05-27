package api

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
)

func GetSettings(c *gin.Context) {
	ok(c, db.AllSettings())
}

func UpdateSettings(c *gin.Context) {
	var body map[string]string
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, 400, err.Error())
		return
	}

	allowed := map[string]func(string) error{
		"reconnect_enabled": func(v string) error {
			if v != "true" && v != "false" {
				return fmt.Errorf("reconnect_enabled phải là true hoặc false")
			}
			return nil
		},
		"reconnect_max_retries": func(v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 100 {
				return fmt.Errorf("reconnect_max_retries phải là số 1-100")
			}
			return nil
		},
		"reconnect_pause_minutes": func(v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 1440 {
				return fmt.Errorf("reconnect_pause_minutes phải là số 1-1440")
			}
			return nil
		},
	}

	for k, v := range body {
		validator, exists := allowed[k]
		if !exists {
			fail(c, 400, fmt.Sprintf("setting %q không hợp lệ", k))
			return
		}
		if err := validator(v); err != nil {
			fail(c, 400, err.Error())
			return
		}
	}

	for k, v := range body {
		if err := db.SetSetting(k, v); err != nil {
			fail(c, 500, err.Error())
			return
		}
	}

	if eventHub != nil {
		eventHub.Publish("settings.updated", body)
	}

	ok(c, db.AllSettings())
}
