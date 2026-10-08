# All users of the organization.
data "select_users" "all" {}

output "user_emails" {
  value = [for user in data.select_users.all.users : user.email]
}

# Only the users with these email addresses. The match ignores case. An email
# that belongs to no user is not an error: it is not in `users`.
locals {
  editor_emails = toset(["alice@example.com", "bob@example.com"])
}

data "select_users" "editors" {
  emails = local.editor_emails
}

locals {
  missing_editors = setsubtract(
    [for email in local.editor_emails : lower(email)],
    [for user in data.select_users.editors.users : lower(user.email)],
  )
}

# Stops the grant when the email is not a SELECT user yet. Without this
# precondition, SELECT accepts the grant, and it takes effect when the person
# first signs in.
resource "select_user_role" "editors" {
  for_each = local.editor_emails

  email = each.value
  role  = "editor"

  lifecycle {
    precondition {
      condition     = !contains(local.missing_editors, lower(each.value))
      error_message = "${each.value} is not a SELECT user. Check the spelling, or ask the person to sign in first."
    }
  }
}
