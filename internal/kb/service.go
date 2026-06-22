package kb

type Service struct {
	// Dependencies are injected in later phases.
}

func NewService() *Service { return &Service{} }
