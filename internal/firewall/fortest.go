package firewall

func WalksForTest() int64 { return walks.Load() }

func PlaceStatsForTest() int64 { return placeStats.Load() }
