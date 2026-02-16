package inputmanager

import (
	"OpenLinkHub/src/common"
	"OpenLinkHub/src/logger"
	"encoding/binary"
	"math"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// InputControlGamepad will emulate input events based on virtual gamepad
func InputControlGamepad(controlType uint16, hold bool) {
	if virtualGamepadFile == nil {
		logger.Log(logger.Fields{}).Error("Virtual keyboard is not present")
		return
	}

	var events []inputEvent

	// Get event key code
	actionType := getInputAction(controlType)
	if actionType == nil {
		return
	}

	// Create events
	events = createInputEvent(actionType.CommandCode, hold)

	// Send events
	for i, event := range events {
		if err := writeVirtualEvent(virtualGamepadFile, &event); err != nil {
			logger.Log(logger.Fields{"error": err}).Error("Failed to emit event")
			return
		}
		if i == 1 && !hold && len(events) > 2 {
			// Delay rapid events
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// InputControlGamepadHold will emulate input events based on virtual gamepad
func InputControlGamepadHold(controlType uint16, press bool) {
	if virtualGamepadFile == nil {
		logger.Log(logger.Fields{}).Error("Virtual keyboard is not present")
		return
	}

	var events []inputEvent

	// Get event key code
	actionType := getInputAction(controlType)
	if actionType == nil {
		return
	}

	// Create events
	events = createInputEventHold(actionType.CommandCode, press)

	// Send events
	for _, event := range events {
		if err := writeVirtualEvent(virtualGamepadFile, &event); err != nil {
			logger.Log(logger.Fields{"error": err}).Error("Failed to emit event")
			return
		}
	}
}

// InputControlGamepadThumbsticks handles thumbstick movement
func InputControlGamepadThumbsticks(b []byte, module uint8, invertAxis bool) {
	x, y, ok := decode2Axis4(b)
	if !ok {
		return
	}

	xAxis := mapAxis(x)
	yAxis := mapAxis(y)

	switch module {
	case 0:
		if invertAxis {
			yAxis = -yAxis
		}
		if err := writeVirtualEvent(virtualGamepadFile, &inputEvent{Type: evAbs, Code: AbsX, Value: xAxis}); err != nil {
			return
		}
		if err := writeVirtualEvent(virtualGamepadFile, &inputEvent{Type: evAbs, Code: AbsY, Value: yAxis}); err != nil {
			return
		}
		break
	case 1:
		if invertAxis {
			yAxis = -yAxis
		}
		if err := writeVirtualEvent(virtualGamepadFile, &inputEvent{Type: evAbs, Code: AbsRx, Value: xAxis}); err != nil {
			return
		}
		if err := writeVirtualEvent(virtualGamepadFile, &inputEvent{Type: evAbs, Code: AbsRy, Value: yAxis}); err != nil {
			return
		}
		break
	}

	err := writeVirtualEvent(virtualGamepadFile, &inputEvent{Type: EvSyn, Code: synReport, Value: 0})
	if err != nil {
		return
	}
}

// InputControlGamepadTriggers handles triggers movement
func InputControlGamepadTriggers(l, r int32, module uint8, deadZoneMin, deadZoneMax uint8) {
	l = applyTriggerDeadzoneMinMax(l, int32(deadZoneMin), int32(deadZoneMax))
	r = applyTriggerDeadzoneMinMax(r, int32(deadZoneMin), int32(deadZoneMax))

	switch module {
	case 0:
		if err := writeVirtualEvent(virtualGamepadFile, &inputEvent{Type: evAbs, Code: AbsZ, Value: l}); err != nil {
			return
		}
		break
	case 1:
		if err := writeVirtualEvent(virtualGamepadFile, &inputEvent{Type: evAbs, Code: AbsRz, Value: r}); err != nil {
			return
		}
		break
	}

	err := writeVirtualEvent(virtualGamepadFile, &inputEvent{Type: EvSyn, Code: synReport, Value: 0})
	if err != nil {
		return
	}
}

// InputControlMoveMouse will move the virtual mouse by the given relative offsets (x, y)
func InputControlMoveMouse(data []byte, invertAxis bool, sensitivityX, sensitivityY uint8) {
	x16, y16, ok := decode2Axis4(data)
	if !ok {
		return
	}
	x := axisToUnit(x16)
	y := axisToUnit(y16)
	x = applyDeadzoneUnit(x, 0.12)
	y = applyDeadzoneUnit(y, 0.12)
	x = applyCurve(x, 1.6)
	y = applyCurve(y, 1.6)

	dx := int32(x * float64(sensitivityX))
	dy := int32(y * float64(sensitivityY))

	if invertAxis {
		dy = -dy
	}

	if virtualMouseFile == nil {
		logger.Log(logger.Fields{}).Error("Virtual mouse is not present")
		return
	}

	var events []inputEvent

	if x != 0 {
		events = append(events, inputEvent{
			Type:  evRel,
			Code:  RelX,
			Value: dx,
		})
	}
	if y != 0 {
		events = append(events, inputEvent{
			Type:  evRel,
			Code:  RelY,
			Value: dy,
		})
	}

	events = append(events, inputEvent{
		Type:  evSyn,
		Code:  0,
		Value: 0,
	})

	for _, event := range events {
		if err := writeVirtualEvent(virtualMouseFile, &event); err != nil {
			logger.Log(logger.Fields{"error": err}).Error("Failed to emit move event")
			return
		}
	}
}

// createVirtualGamepad will create new virtual gamepad device
func createVirtualGamepad(vendorId, productId uint16) error {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY, 0660)
	if err != nil {
		logger.Log(logger.Fields{"error": err}).Error("Failed to open /dev/uinput")
		return err
	}
	virtualGamepadFile = f
	virtualGamepadPointer = f.Fd()

	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, virtualGamepadPointer, syscall.F_SETFL, syscall.O_NONBLOCK)
	if errno != 0 {
		logger.Log(logger.Fields{"error": errno}).Error("Unable to set non-blocking mode")
	}

	u := uInputUserDev{
		ID: inputID{
			BusType: 0x06, // BUS_VIRTUAL
			Vendor:  vendorId,
			Product: productId,
			Version: 1,
		},
		FFEffects: 0,
	}
	copy(u.Name[:], "OpenLinkHub Virtual Gamepad")

	if _, _, errno = syscall.Syscall(syscall.SYS_IOCTL, virtualGamepadPointer, UiSetEvbit, uintptr(evKey)); errno != 0 {
		return errno
	}
	if _, _, errno = syscall.Syscall(syscall.SYS_IOCTL, virtualGamepadPointer, UiSetEvbit, uintptr(evAbs)); errno != 0 {
		return errno
	}
	if _, _, errno = syscall.Syscall(syscall.SYS_IOCTL, virtualGamepadPointer, UiSetEvbit, uintptr(evSyn)); errno != 0 {
		return errno
	}

	for _, code := range controllerButtons {
		if _, _, errno = syscall.Syscall(syscall.SYS_IOCTL, virtualGamepadPointer, UiSetKeybit, uintptr(code)); errno != 0 {
			logger.Log(logger.Fields{"error": errno, "code": code}).Error("Failed to enable BTN code")
		}
	}

	// Axes: sticks + hat + triggers
	absAxes := []uint16{AbsX, AbsY, AbsRx, AbsRy, AbsHat0X, AbsHat0Y, AbsZ, AbsRz}
	for _, a := range absAxes {
		if _, _, errno = syscall.Syscall(syscall.SYS_IOCTL, virtualGamepadPointer, UiSetAbsbit, uintptr(a)); errno != 0 {
			logger.Log(logger.Fields{"error": errno, "abs": a}).Error("Failed to enable ABS axis")
		}
	}

	setAbsRange(&u, int(AbsX), -32768, 32767)
	setAbsRange(&u, int(AbsY), -32768, 32767)
	setAbsRange(&u, int(AbsRx), -32768, 32767)
	setAbsRange(&u, int(AbsRy), -32768, 32767)
	setAbsRange(&u, int(AbsHat0X), -1, 1)
	setAbsRange(&u, int(AbsHat0Y), -1, 1)
	setAbsRange(&u, int(AbsZ), 0, 1023)
	setAbsRange(&u, int(AbsRz), 0, 1023)

	// Write struct
	if _, e := f.Write((*(*[unsafe.Sizeof(u)]byte)(unsafe.Pointer(&u)))[:]); e != nil {
		logger.Log(logger.Fields{"error": e}).Error("Failed to write virtual gamepad data struct")
		return e
	}

	// Create device
	if _, _, errno = syscall.Syscall(syscall.SYS_IOCTL, virtualGamepadPointer, UiDevCreate, 0); errno != 0 {
		logger.Log(logger.Fields{"error": errno}).Error("Failed to create virtual gamepad")
		return errno
	}

	return nil
}

// setAbsRange will set minimum and maximum ranges
func setAbsRange(u *uInputUserDev, abs int, min, max int32) {
	u.AbsMin[abs] = min
	u.AbsMax[abs] = max
	u.AbsFuzz[abs] = 0
	u.AbsFlat[abs] = 0
}

// destroyVirtualGamepad will destroy virtual gamepad and close uinput device
func destroyVirtualGamepad() {
	if virtualGamepadPointer != 0 {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, virtualGamepadPointer, UiDevDestroy, 0); errno != 0 {
			logger.Log(logger.Fields{"error": errno}).Error("Failed to destroy virtual keyboard")
		}

		if err := virtualGamepadFile.Close(); err != nil {
			logger.Log(logger.Fields{"error": err}).Error("Failed to close /dev/uinput")
			return
		}
	}
}

// decode2Axis4 will decode byte slice into X and Y
func decode2Axis4(b []byte) (x, y int16, ok bool) {
	if len(b) < 4 {
		return 0, 0, false
	}
	x = int16(binary.LittleEndian.Uint16(b[0:2]))
	y = int16(binary.LittleEndian.Uint16(b[2:4]))
	return x, y, true
}

// mapAxis will clamp given value with min / max
func mapAxis(raw int16) int32 {
	return common.ClampInt32(int32(raw), -32768, 32767)
}

// axisToUnit will convert raw axis to units
func axisToUnit(raw int16) float64 {
	if raw < 0 {
		return float64(raw) / 32768.0
	}
	return float64(raw) / 32767.0
}

// applyDeadzoneUnit will apply deadzone for mouse area
func applyDeadzoneUnit(v float64, dz float64) float64 {
	if v > -dz && v < dz {
		return 0
	}
	if v > 0 {
		return (v - dz) / (1 - dz)
	}
	return (v + dz) / (1 - dz)
}

// applyCurve will apply mouse curve
func applyCurve(v float64, gamma float64) float64 {
	if v == 0 {
		return 0
	}
	sign := 1.0
	if v < 0 {
		sign = -1.0
		v = -v
	}
	return sign * math.Pow(v, gamma)
}

func applyTriggerDeadzoneMinMax(v int32, minPct, maxPct int32) int32 {
	v = common.ClampInt32(v, 0, 1023)
	if minPct < 2 {
		minPct = 2
	}
	if minPct > 15 {
		minPct = 15
	}
	if maxPct < 2 {
		maxPct = 2
	}
	if maxPct > 15 {
		maxPct = 15
	}

	inMin := (1023 * minPct) / 100
	inMax := 1023 - (1023*maxPct)/100
	if inMax <= inMin {
		if v <= inMin {
			return 0
		}
		return 1023
	}

	if v <= inMin {
		return 0
	}
	if v >= inMax {
		return 1023
	}
	out := ((v - inMin) * 1023) / (inMax - inMin)
	return common.ClampInt32(out, 0, 1023)
}
