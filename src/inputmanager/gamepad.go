package inputmanager

import (
	"OpenLinkHub/src/common"
	"OpenLinkHub/src/logger"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type rumbleEffect struct {
	Strong uint16
	Weak   uint16
	Length uint16
}

type uinputFFUpload struct {
	RequestID uint32
	Retval    int32
	Effect    ffEffect
	Old       ffEffect
}

type uinputFFErase struct {
	RequestID uint32
	Retval    int32
	EffectID  uint32
}

type ffTrigger struct {
	Button   uint16
	Interval uint16
}

type ffReplay struct {
	Length uint16
	Delay  uint16
}

type ffEffect struct {
	Type      uint16
	ID        int16
	Direction uint16
	Trigger   ffTrigger
	Replay    ffReplay
	U         [8]uint32
}

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
	f, err := os.OpenFile("/dev/uinput", os.O_RDWR|syscall.O_NONBLOCK, 0660)
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
		FFEffects: 16,
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
	if _, _, errno = syscall.Syscall(syscall.SYS_IOCTL, virtualGamepadPointer, UiSetEvbit, uintptr(evFf)); errno != 0 {
		return errno
	}
	if _, _, errno = syscall.Syscall(syscall.SYS_IOCTL, virtualGamepadPointer, UiSetFfbit, uintptr(ffRumble)); errno != 0 {
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
	running = true

	uinputFFBridge(virtualGamepadFile, func(strong, weak uint16, lengthMs uint16, playing bool) {
		var left, right byte
		if playing {
			left = magToByte(strong)
			right = magToByte(weak)
		} else {
			left, right = 0, 0
		}

		if len(gamepadSerial) > 0 {
			dispatch(gamepadSerial, "TriggerHapticEngineExternal", left, right)
		}

		rumbleMutex.Lock()
		lastLeft = left
		lastRight = right
		rumbleGen++
		currentGen := rumbleGen
		rumbleMutex.Unlock()

		// If game never sends 0,0 for L/R we need to reset haptics in order to prevent infinite vibration
		if playing && (left != 0 || right != 0) {
			d := time.Duration(lengthMs) * time.Millisecond
			if lengthMs == 0 || lengthMs == 0xFFFF {
				d = time.Duration(fallbackMs) * time.Millisecond
			}
			go func(expectedGen uint64, wait time.Duration) {
				time.Sleep(wait)

				rumbleMutex.Lock()
				sc := (rumbleGen == expectedGen) && (lastLeft != 0 || lastRight != 0)
				rumbleMutex.Unlock()

				if sc && len(gamepadSerial) > 0 {
					dispatch(gamepadSerial, "TriggerHapticEngineExternal", byte(0), byte(0))

					rumbleMutex.Lock()
					lastLeft, lastRight = 0, 0
					rumbleGen++
					rumbleMutex.Unlock()
				}
			}(currentGen, d)
		}
	})

	return nil
}

// magToByte converts a 16-bit force-feedback magnitude (0..65535) to an 8-bit value (0..255).
func magToByte(m uint16) byte {
	return byte((uint32(m) * 255) / 65535)
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
		running = false
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

// applyTriggerDeadzoneMinMax will apply inner (minPct) and outer (maxPct) dead zones
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

// rumbleMagnitudes is two u16 at start of union
func (e *ffEffect) rumbleMagnitudes() (strong, weak uint16) {
	u0 := e.U[0]
	strong = uint16(u0 & 0xFFFF)
	weak = uint16((u0 >> 16) & 0xFFFF)
	return
}

// uinputFFBridge will open virtual gamepad device for reading
func uinputFFBridge(uinput *os.File, onRumble func(uint16, uint16, uint16, bool)) {
	fd := uinput.Fd()
	var mu sync.Mutex
	effects := map[int16]rumbleEffect{}

	go func() {
		for {
			if !running {
				return
			}

			var ev inputEvent
			buf := (*(*[unsafe.Sizeof(ev)]byte)(unsafe.Pointer(&ev)))[:]

			n, err := uinput.Read(buf)
			if err != nil {
				if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
					time.Sleep(2 * time.Millisecond)
					continue
				}
				return
			}
			if n != int(unsafe.Sizeof(ev)) {
				continue
			}

			switch ev.Type {
			case EvUinput:
				switch ev.Code {
				case UiFfUpload:
					var up uinputFFUpload
					up.RequestID = uint32(ev.Value)

					_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uIBeginFFUpload, uintptr(unsafe.Pointer(&up)))
					logger.Log(logger.Fields{"error": errno}).Error("gamepad uIBeginFFUpload failed")
					if errno != 0 {
						up.Retval = -int32(syscall.EINVAL)
						_, _, errno2 := syscall.Syscall(syscall.SYS_IOCTL, fd, uIEndFFUpload, uintptr(unsafe.Pointer(&up)))
						logger.Log(logger.Fields{"error": errno2}).Error("gamepad uIEndFFUpload failed")
						continue
					}
					up.Effect.rumbleMagnitudes()
					_, _, errno3 := syscall.Syscall(syscall.SYS_IOCTL, fd, uIEndFFUpload, uintptr(unsafe.Pointer(&up)))
					logger.Log(logger.Fields{"error": errno3}).Error("gamepad uIEndFFUpload failed")

					if up.Effect.Type == FfRumble {
						strong, weak := up.Effect.rumbleMagnitudes()
						length := up.Effect.Replay.Length
						id := up.Effect.ID

						mu.Lock()
						effects[id] = rumbleEffect{Strong: strong, Weak: weak, Length: length}
						mu.Unlock()

						up.Retval = 0
					} else {
						up.Retval = -int32(syscall.EOPNOTSUPP)
					}
					_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, uIEndFFUpload, uintptr(unsafe.Pointer(&up)))

				case UiFfErase:
					var er uinputFFErase
					er.RequestID = uint32(ev.Value)

					_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uIBeginFFErase, uintptr(unsafe.Pointer(&er)))
					if errno != 0 {
						er.Retval = -int32(syscall.EINVAL)
						_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, uIEndFFErase, uintptr(unsafe.Pointer(&er)))
						continue
					}

					mu.Lock()
					delete(effects, int16(er.EffectID))
					mu.Unlock()

					er.Retval = 0
					_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, uIEndFFErase, uintptr(unsafe.Pointer(&er)))
				}
			case EvFf:
				id := int16(ev.Code)

				mu.Lock()
				eff, ok := effects[id]
				mu.Unlock()

				if ev.Value == 0 {
					onRumble(0, 0, 0, false)
					continue
				}

				if !ok {
					onRumble(0, 0, 0, false)
					continue
				}

				if eff.Strong == 0 && eff.Weak == 0 {
					onRumble(0, 0, 0, false)
					continue
				}
				onRumble(eff.Strong, eff.Weak, eff.Length, true)
			}
		}
	}()
}
