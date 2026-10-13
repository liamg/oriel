// A Nomad job.
job "web" {
  datacenters = ["dc1"]
  type        = "service"

  group "app" {
    count = 3
    task "server" {
      driver = "docker"
      config {
        image = "nginx:1.27"
        ports = ["http"]
      }
      resources {
        cpu    = 500
        memory = 256
      }
    }
  }
}
