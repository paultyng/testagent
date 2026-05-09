package claude

import "fmt"

// ExitError carries a non-zero exit code out of a cobra RunE without calling
// os.Exit inside the handler (which would bypass cobra's deferred cleanup
// and make RunE untestable in-process). The root main inspects the error
// returned from cobra.Execute and exits with the carried code.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}
