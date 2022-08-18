package declarative

func WithResourceLabels(labels map[string]string) reconcilerOption {
	return func(o manifestOptions) manifestOptions {

		for key, value := range labels {
			o.resourceLabels[key] = value
		}

		return o
	}
}
