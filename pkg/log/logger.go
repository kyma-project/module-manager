package log

import (
	"os"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	zap2 "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func ConfigLogger(level int8) logr.Logger {
	if level > 0 {
		level = -level
	}
	// The following settings is based on kyma community Improvement of log messages usability
	//nolint:lll
	// https://github.com/kyma-project/community/blob/main/concepts/observability-consistent-logging/improvement-of-log-messages-usability.md#log-structure
	atomicLevel := zap.NewAtomicLevelAt(zapcore.Level(level))
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "date"
	encoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	core := zapcore.NewCore(
		&zap2.KubeAwareEncoder{Encoder: zapcore.NewJSONEncoder(encoderConfig)}, zapcore.Lock(os.Stdout), atomicLevel,
	)
	zapLog := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	logger := zapr.NewLogger(zapLog.With(zap.Namespace("context")))

	return logger
}
