package observability

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	"go.elastic.co/ecslogrus"
)

func init() {
	log.SetFormatter(&ecslogrus.Formatter{})
}

func SetLogLevel(logLevel string) error {
	if logLevel == "" {
		logLevel = "info"
	}
	lvl, err := log.ParseLevel(logLevel)
	if err != nil {
		return fmt.Errorf("invalid log level %q: %w", logLevel, err)
	}
	log.SetLevel(lvl)
	return nil
}
