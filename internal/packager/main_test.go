package packager

import (
	"os"
	"speedapp-packager/internal/buildprocess"
	"testing"
)

func TestMain(m *testing.M) { buildprocess.HandleInvocation(); os.Exit(m.Run()) }
