package log

import (
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
)

func ConfigLogger() logr.Logger {
	// The following settings is based on kyma community Improvement of log messages usability
	//nolint:lll
	// https://github.com/kyma-project/community/blob/main/concepts/observability-consistent-logging/improvement-of-log-messages-usability.md#log-structure
	atomicLevel := zap.NewAtomicLevel()
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "date"
	encoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), zapcore.Lock(os.Stdout), atomicLevel)
	zapLog := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	logger := zapr.NewLogger(zapLog.With(zap.Namespace("context")))
	return logger
}
