package seer

// Usage Types
type ServiceType string

type UsageReturn struct {
	Id            string
	Name          string
	Type          []string
	Timestamp     int
	UsedMem       int
	TotalMem      int
	FreeMem       int
	TotalCpu      int
	CpuCount      int
	CpuUser       int
	CpuNice       int
	CpuSystem     int
	CpuIdle       int
	CpuIowait     int
	CpuIrq        int
	CpuSoftirq    int
	CpuSteal      int
	CpuGuest      int
	CpuGuestNice  int
	CpuStatCount  int
	Address       string
	TotalDisk     int
	FreeDisk      int
	UsedDisk      int
	AvailableDisk int
	CustomValues  map[string]float64
}

type Cpu struct {
	Total     uint64 `cbor:"21,keyasint"`
	Count     int    `cbor:"22,keyasint"`
	User      uint64 `cbor:"23,keyasint"`
	Nice      uint64 `cbor:"24,keyasint"`
	System    uint64 `cbor:"25,keyasint"`
	Idle      uint64 `cbor:"26,keyasint"`
	Iowait    uint64 `cbor:"27, keyasint"`
	Irq       uint64 `cbor:"28,keyasint"`
	Softirq   uint64 `cbor:"29,keyasint"`
	Steal     uint64 `cbor:"30,keyasint"`
	Guest     uint64 `cbor:"31,keyasint"`
	GuestNice uint64 `cbor:"32,keyasint"`
	StatCount int    `cbor:"33,keyasint"`
}

type UsageData struct {
	Memory       Memory             `cbor:"3,keyasint"`
	Cpu          Cpu                `cbor:"5,keyasint"`
	Disk         Disk               `cbor:"7,keyasint"`
	CustomValues map[string]float64 `cbor:"9,keyasint,omitempty"`
}

type Memory struct {
	Used  uint64 `cbor:"11,keyasint"`
	Total uint64 `cbor:"12,keyasint"`
	Free  uint64 `cbor:"13,keyasint"`
}

type Disk struct {
	Total     uint64 `cbor:"14,keyasint"`
	Free      uint64 `cbor:"15,keyasint"`
	Used      uint64 `cbor:"16,keyasint"`
	Available uint64 `cbor:"17,keyasint"`
}

type ServiceInfo struct {
	Type ServiceType
	Meta map[string]string
}

type Services []ServiceInfo

// GeoBeacon Types
type Peer struct {
	Id       string
	Location PeerLocation
}

type Location struct {
	Latitude  float32 `cbor:"1,keyasint" yaml:"lat"`
	Longitude float32 `cbor:"2,keyasint" yaml:"long"`
}

type PeerLocation struct {
	Timestamp int64    `cbor:"1,keyasint"`
	Location  Location `cbor:"4,keyasint"`
}
