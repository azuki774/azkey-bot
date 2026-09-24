// Package domain contains values shared by the bot layers.
package domain

import (
	"context"
	"time"
)

// RequestLimiter coordinates API traffic across read and write operations.
type RequestLimiter interface {
	Wait(context.Context) error
	SetCooldown(time.Time)
}
