package dispatcher

// Package: dispatcher
// Author: Nikola Jurkovic
// License: GPL-3.0 or later

import "reflect"

type DeviceDispatcher func(
	deviceId string,
	methodName string,
	args ...interface{},
) []reflect.Value
