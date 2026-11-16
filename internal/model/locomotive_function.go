package model

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const MaxLocomotiveFunctionNumber = 68

// LocomotiveFunction describes configuration, not the current enabled state.
type LocomotiveFunction struct {
	Number    int  `json:"number"`
	Momentary bool `json:"momentary"`
}

func (f *LocomotiveFunction) UnmarshalJSON(data []byte) error {
	var input struct {
		Number    *int `json:"number"`
		Momentary bool `json:"momentary"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return err
	}
	if input.Number == nil {
		return fmt.Errorf("function number is required")
	}
	f.Number, f.Momentary = *input.Number, input.Momentary
	return nil
}

func ValidateLocomotiveFunctions(functions []LocomotiveFunction) error {
	seen := make(map[int]bool, len(functions))
	for _, function := range functions {
		if function.Number < 0 || function.Number > MaxLocomotiveFunctionNumber {
			return fmt.Errorf("function number must be in range 0..%d", MaxLocomotiveFunctionNumber)
		}
		if seen[function.Number] {
			return fmt.Errorf("duplicate function number %d", function.Number)
		}
		seen[function.Number] = true
	}
	return nil
}

func (l Locomotive) IsMomentary(number int) bool {
	for _, function := range l.Functions {
		if function.Number == number {
			return function.Momentary
		}
	}
	return false
}
