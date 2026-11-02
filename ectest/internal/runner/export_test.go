package runner

// Test access to the unexported write paths.
func (r *Runner) ManualForTest(ch int) error { return r.manual(ch) }

func (r *Runner) WriteForTest(ch, duty int) error {
	_, err := r.write(ch, duty)
	return err
}
