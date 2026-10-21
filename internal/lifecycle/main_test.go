package lifecycle

import (
	"os"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

func TestMain(m *testing.M) { os.Exit(testsupport.Main(m)) }
