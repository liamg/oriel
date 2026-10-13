terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

variable "region" {
  type    = string
  default = "eu-west-2"
}

# One bucket per environment.
resource "aws_s3_bucket" "logs" {
  for_each = toset(["dev", "prod"])
  bucket   = "logs-${each.key}-${var.region}"
  tags = {
    Name = upper(each.key)
  }
}

output "bucket_ids" {
  value = [for b in aws_s3_bucket.logs : b.id]
}
