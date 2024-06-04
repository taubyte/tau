package counters

import (
	"github.com/taubyte/tau/core/services/substrate/counters"
	"github.com/taubyte/tau/services/substrate/mocks/counters/report"
)

/********************************  Interface Methods *************************/

func (c *counter) Push(wms ...*counters.WrappedMetric) {
	for _, wm := range wms {
		c.metricChan <- wm
	}
}

func (*counter) Implemented() bool {
	return true
}

func (c *counter) Close() error {
	c.ledgerLock.Lock()
	for k := range c.ledger {
		delete(c.ledger, k)
	}

	close(c.metricChan)
	c.ledgerLock.Unlock()
	return nil
}

/********************************  Pointer Methods ********8************************/

func (c *counter) Dump() report.MetricMap {
	temp := c.ledger
	c.ledgerLock.Lock()
	c.ledger = make(map[string]counters.Metric)
	c.ledgerLock.Unlock()

	return temp
}

func (c *counter) Start() {
	go func() {
		for {
			select {
			case <-c.Service.Context().Done():
				return

			case metric := <-c.metricChan:
				if metric != nil {
					c.ledgerLock.Lock()
					genericMetric, ok := c.ledger[metric.Key]
					if ok {
						if err := genericMetric.Aggregate(metric.Metric); err != nil {
							logger.Errorf("aggregate metric failed with: %s", err)
						}
					} else {
						c.ledger[metric.Key] = metric.Metric
					}
					c.ledgerLock.Unlock()
				}
			}
		}
	}()
}
