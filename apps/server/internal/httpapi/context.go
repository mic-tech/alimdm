package httpapi

import (
	"context"

	"ali-mdm/server/internal/store"
)

type ctxKey int

const (
	deviceKey   ctxKey = 1
	operatorKey ctxKey = 2
)

func withDevice(ctx context.Context, d *store.Device) context.Context {
	return context.WithValue(ctx, deviceKey, d)
}

func deviceFrom(ctx context.Context) *store.Device {
	d, _ := ctx.Value(deviceKey).(*store.Device)
	return d
}

func withOperator(ctx context.Context, o *store.Operator) context.Context {
	return context.WithValue(ctx, operatorKey, o)
}

func operatorFrom(ctx context.Context) *store.Operator {
	o, _ := ctx.Value(operatorKey).(*store.Operator)
	return o
}
