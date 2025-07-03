package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/flipped-aurora/gin-vue-admin/server/global"
	"github.com/flipped-aurora/gin-vue-admin/server/model/common/response"
	"github.com/gin-gonic/gin"
)

type LimitConfig struct {
	// GenerationKey 根据业务生成key 下面CheckOrMark查询生成
	GenerationKey func(c *gin.Context) string
	// 检查函数,用户可修改具体逻辑,更加灵活
	CheckOrMark func(key string, expire int, limit int) error
	// Expire key 过期时间
	Expire int
	// Limit 周期时间
	Limit int
}

func (l LimitConfig) LimitWithTime() gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := l.CheckOrMark(l.GenerationKey(c), l.Expire, l.Limit); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": response.ERROR, "msg": err.Error()})
			c.Abort()
			return
		} else {
			c.Next()
		}
	}
}

// DefaultGenerationKey 默认生成key
func DefaultGenerationKey(c *gin.Context) string {
	return "GVA_Limit" + c.ClientIP()
}

func DefaultCheckOrMark(key string, expire int, limit int) (err error) {
	// 判断是否开启redis
	if global.GVA_REDIS == nil {
		return err
	}
	if err = SetLimitWithTime(key, limit, time.Duration(expire)*time.Second); err != nil {
		global.GVA_LOG.Error("limit", zap.Error(err))
	}
	return err
}

func DefaultLimit() gin.HandlerFunc {
	return LimitConfig{
		GenerationKey: DefaultGenerationKey,
		CheckOrMark:   DefaultCheckOrMark,
		Expire:        global.GVA_CONFIG.System.LimitTimeIP,
		Limit:         global.GVA_CONFIG.System.LimitCountIP,
	}.LimitWithTime()
}

// SetLimitWithTime 设置访问次数 - 修复了原有的race condition问题
func SetLimitWithTime(key string, limit int, expiration time.Duration) error {
	ctx := context.Background()
	
	// 使用Lua脚本确保原子性操作，避免race condition
	luaScript := `
	local key = KEYS[1]
	local limit = tonumber(ARGV[1])
	local expiration = tonumber(ARGV[2])
	
	local current = redis.call('GET', key)
	if current == false then
		redis.call('SET', key, 1)
		redis.call('EXPIRE', key, expiration)
		return 1
	else
		current = tonumber(current)
		if current < limit then
			return redis.call('INCR', key)
		else
			local ttl = redis.call('TTL', key)
			return -ttl
		end
	end
	`
	
	result, err := global.GVA_REDIS.Eval(ctx, luaScript, []string{key}, limit, int(expiration.Seconds())).Result()
	if err != nil {
		return err
	}
	
	// 如果返回值是负数，表示已超过限制，返回相应错误
	if resultInt, ok := result.(int64); ok && resultInt < 0 {
		ttl := -resultInt
		if ttl <= 0 {
			return errors.New("请求太过频繁，请稍后再试")
		}
		return errors.New("请求太过频繁, 请 " + time.Duration(ttl)*time.Second.String() + " 后尝试")
	}
	
	return nil
}
