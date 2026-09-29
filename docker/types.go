package docker_cmd

type DockerPs struct {
	Command      string
	CreatedAt    string
	HealthStatus string
	ID           string
	Image        string
	Labels       string
	LocalVolumes string
	Mounts       string
	Names        string
	Networks     string
	Platform     PlatformData
	Ports        string
	RunningFor   string
	Size         string
	State        string
	Status       string
}

type PlatformData struct {
	Architecture string
	OS           string
}

type DockerStats struct {
	ID       string
	CPUPerc  string
	MemUsage string
}
