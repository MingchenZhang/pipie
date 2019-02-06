package pipie

const TransferFrameSize = 65

type TransferFrame struct {
	TransferVersion uint8
	Source          [32]byte
	Destination     [32]byte
	// followed by exchange message
}
