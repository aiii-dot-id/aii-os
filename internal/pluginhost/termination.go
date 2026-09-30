package pluginhost

import "time"

func (ap *ActivePlugin) Terminated() <-chan struct{} {
	if ap.sup == nil {
		return nil
	}
	return ap.sup.Terminated()
}

func (ap *ActivePlugin) Failure() ([]time.Time, error) {
	if ap.sup == nil {
		return nil, nil
	}
	return ap.sup.Failure()
}
