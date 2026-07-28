package terminal

const maxBufferedOSCBytes = 64 * 1024

type outputFilterState uint8

const (
	outputText outputFilterState = iota
	outputEscape
	outputOSC
	outputOSCEscape
)

type outputFilter struct {
	state    outputFilterState
	sequence []byte
}

func (f *outputFilter) Filter(input []byte) []byte {
	output := make([]byte, 0, len(input))
	for _, value := range input {
		switch f.state {
		case outputText:
			if value == 0x1b {
				f.sequence = append(f.sequence[:0], value)
				f.state = outputEscape
			} else {
				output = append(output, value)
			}

		case outputEscape:
			if value == 0x1b {
				output = append(output, f.sequence...)
				f.sequence = append(f.sequence[:0], value)
				continue
			}
			f.sequence = append(f.sequence, value)
			if value == ']' {
				f.state = outputOSC
			} else {
				output = append(output, f.sequence...)
				f.reset()
			}

		case outputOSC:
			f.sequence = append(f.sequence, value)
			switch value {
			case 0x07:
				f.finishOSC(&output)
			case 0x1b:
				f.state = outputOSCEscape
			}

		case outputOSCEscape:
			f.sequence = append(f.sequence, value)
			if value == '\\' {
				f.finishOSC(&output)
			} else if value != 0x1b {
				f.state = outputOSC
			}
		}

		if len(f.sequence) > maxBufferedOSCBytes {
			output = append(output, f.sequence...)
			f.reset()
		}
	}
	return output
}

func (f *outputFilter) finishOSC(output *[]byte) {
	if !isTitleOSC(f.sequence) {
		*output = append(*output, f.sequence...)
	}
	f.reset()
}

func (f *outputFilter) reset() {
	f.sequence = f.sequence[:0]
	f.state = outputText
}

func isTitleOSC(sequence []byte) bool {
	const payloadStart = 2
	if len(sequence) <= payloadStart+1 || sequence[0] != 0x1b || sequence[1] != ']' {
		return false
	}
	payload := sequence[payloadStart:]
	return len(payload) >= 2 && payload[1] == ';' &&
		(payload[0] == '0' || payload[0] == '1' || payload[0] == '2')
}
