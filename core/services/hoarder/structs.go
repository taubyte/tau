package hoarder

type AuctionType int

type MetaType int

const (
	Database MetaType = iota
	Storage
)

const (
	AuctionNew AuctionType = iota
	AuctionIntent
	AuctionOffer
	AuctionEnd
)

type Auction struct {
	Type     AuctionType
	MetaType MetaType
	Meta     MetaData
	Lottery  Lottery
}

type MetaData struct {
	ConfigId      string
	ProjectId     string
	ApplicationId string
	Match         string
	Branch        string
}

type Lottery struct {
	HoarderId string
	Number    uint64
}
