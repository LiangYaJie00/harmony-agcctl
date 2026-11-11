package agc

import "github.com/LiangYaJie00/harmony-agcctl/internal/apperr"

func requestInvalid(format string, args ...any) *apperr.Error {
	return apperr.RequestInvalid(format, args...)
}
