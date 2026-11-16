package packaging

import (
	"os"
	"speedapp-packager/app/packager/internal/buildprocess"
	"testing"
)

func TestMain(m *testing.M) { buildprocess.HandleInvocation(); os.Exit(m.Run()) }
