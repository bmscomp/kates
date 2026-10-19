package conformity

import "fmt"

func init() {
	register(check{code: "waiver-expired", severity: Warning, run: expiredWaivers})
}

// expiredWaivers warns of a waiver whose date has passed. The API accepts
// it, and the rule it names counts towards the verdict again.
func expiredWaivers(c *ctx) []finding {
	var fs []finding
	for rule, w := range c.waivers {
		if !w.active(c.today) {
			fs = append(fs, finding{
				node: w.node,
				path: fmt.Sprintf("spec.waivers[%d].until", w.index),
				message: fmt.Sprintf("the waiver expired on %s, so %s counts again: renew the date or remove the waiver",
					w.until.Format("2006-01-02"), rule),
			})
		}
	}
	return fs
}
