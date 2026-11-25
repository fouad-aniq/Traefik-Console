package engine

import "context"

// initializeDomainFile writes the console domains file once, the first time the
// console runs. A file that is missing or different later is reported as drift
// (and fixed by restore or adopt), never silently rewritten here.
func (c *operations) initializeDomainFile(ctx context.Context) error {
	b, err := c.files.ReadConsoleDomainsConfig()
	if err != nil {
		return err
	}
	if b == nil {
		initialized, err := c.store.GetSetting(ctx, "console_domains_initialized")
		if err != nil {
			return err
		}
		if initialized == "1" {
			return nil
		}
		rows, err := c.store.ListAllItems(ctx)
		if err != nil {
			return err
		}
		if b, err = renderDomains(rows); err != nil {
			return err
		}
		if err = c.files.WriteConsoleDomainsConfig(b); err != nil {
			return err
		}
	}
	return c.store.SetSetting(ctx, "console_domains_initialized", "1")
}
