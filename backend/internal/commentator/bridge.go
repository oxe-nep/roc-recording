package commentator

// PlayoutBridge resolves DeckLink playout sink settings for a channel id.
type PlayoutBridge interface {
	Sink(id int) (device, formatCode string, err error)
	ResolveOpenDevice(device string) string
	LookupDeviceOpen(device string) string
	OutputTiming(formatCode string) (w, h int, fps float64, interlaced bool, err error)
}
