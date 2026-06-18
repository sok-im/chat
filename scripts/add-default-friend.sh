#!/usr/bin/env bash
# Manage default friends via Admin API.
#
# Usage:
#   ./scripts/add-default-friend.sh [create]          Auto-create account and add as default friend
#   ./scripts/add-default-friend.sh <userID> [...]    Add existing users as default friends
#   ./scripts/add-default-friend.sh list              List all default friends
#   USER_IDS="id1,id2" ./scripts/add-default-friend.sh
#
# Environment:
#   ADMIN_API_URL              Admin API base URL (default: http://127.0.0.1:10009)
#   ADMIN_ACCOUNT              Admin account (default: chatAdmin)
#   ADMIN_PASSWORD             Admin password (default: md5 hex of ADMIN_ACCOUNT)
#   DEFAULT_FRIEND_NICKNAME    Nickname prefix (default: 默认好友)
#   DEFAULT_FRIEND_FACE_URL    Avatar URL (optional)
#   DEFAULT_FRIEND_PASSWORD    Login password (optional, auto-generated if empty)
#
# Example:
#   ./scripts/add-default-friend.sh create
#   ./scripts/add-default-friend.sh 1234567890
#   ./scripts/add-default-friend.sh list

set -euo pipefail

ADMIN_API_URL="${ADMIN_API_URL:-http://127.0.0.1:10009}"
ADMIN_ACCOUNT="${ADMIN_ACCOUNT:-chatAdmin}"
DEFAULT_FRIEND_NICKNAME="${DEFAULT_FRIEND_NICKNAME:-默认好友}"

if ! command -v jq >/dev/null 2>&1; then
  echo "error: jq is required" >&2
  exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "error: python3 is required" >&2
  exit 1
fi

if [[ -z "${ADMIN_PASSWORD:-}" ]]; then
  if command -v md5sum >/dev/null 2>&1; then
    ADMIN_PASSWORD="$(printf '%s' "$ADMIN_ACCOUNT" | md5sum | awk '{print $1}')"
  elif command -v md5 >/dev/null 2>&1; then
    ADMIN_PASSWORD="$(printf '%s' "$ADMIN_ACCOUNT" | md5 | awk '{print $NF}')"
  else
    ADMIN_PASSWORD="$(python3 -c "import hashlib; print(hashlib.md5('${ADMIN_ACCOUNT}'.encode()).hexdigest())")"
  fi
fi

admin_login() {
  local login_resp admin_token
  login_resp="$(curl -sS -X POST "${ADMIN_API_URL}/account/login" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg account "$ADMIN_ACCOUNT" --arg password "$ADMIN_PASSWORD" \
      '{account: $account, password: $password}')")"

  if [[ "$(echo "$login_resp" | jq -r '.errCode')" != "0" ]]; then
    echo "login failed: $(echo "$login_resp" | jq -c '.')" >&2
    return 1
  fi

  admin_token="$(echo "$login_resp" | jq -r '.data.adminToken')"
  if [[ -z "$admin_token" || "$admin_token" == "null" ]]; then
    echo "login failed: adminToken missing" >&2
    return 1
  fi
  printf '%s' "$admin_token"
}

search_default_friends() {
  local admin_token="$1"
  local keyword="${2:-}"
  curl -sS -X POST "${ADMIN_API_URL}/default/user/search" \
    -H "Content-Type: application/json" \
    -H "token: ${admin_token}" \
    -d "$(jq -n --arg keyword "$keyword" \
      '{keyword: $keyword, pagination: {pageNumber: 1, showNumber: 1000}}')"
}

print_friend_table() {
  local users_json="$1"
  local count
  count="$(echo "$users_json" | jq 'length')"
  if [[ "$count" -eq 0 ]]; then
    echo "no default friends found"
    return
  fi

  printf '%-14s %-24s %-12s %s\n' "USER_ID" "NICKNAME" "ACCOUNT" "FACE_URL"
  printf '%.0s-' {1..90}
  echo
  echo "$users_json" | jq -r '.[] |
    [
      (.userID // "-"),
      ((.user.nickname // "-") | if length > 22 then .[0:21] + "…" else . end),
      ((.user.account // "-") | if length > 10 then .[0:9] + "…" else . end),
      ((.user.faceURL // "-") | if length > 40 then .[0:39] + "…" else . end)
    ] | @tsv' | while IFS=$'\t' read -r uid nick acct face; do
    printf '%-14s %-24s %-12s %s\n' "$uid" "$nick" "$acct" "$face"
  done
  echo
  echo "total: $count"
}

print_friend_details() {
  local users_json="$1"
  echo "$users_json" | jq .
}

gen_default_friend_account() {
  DEFAULT_FRIEND_PASSWORD="${DEFAULT_FRIEND_PASSWORD:-}" \
  DEFAULT_FRIEND_NICKNAME="${DEFAULT_FRIEND_NICKNAME}" \
  python3 - <<'PY'
import json
import os
import random
import string
import time

nickname_prefix = os.environ.get("DEFAULT_FRIEND_NICKNAME", "默认好友")
password = os.environ.get("DEFAULT_FRIEND_PASSWORD") or "".join(
    random.choices(string.ascii_letters + string.digits, k=12)
)
user_id = str(random.randint(1, 9)) + "".join(str(random.randint(0, 9)) for _ in range(9))
account = "df" + str(int(time.time())) + "".join(random.choices(string.ascii_lowercase, k=3))
nickname = f"{nickname_prefix}{random.randint(1000, 9999)}"

print(json.dumps({
    "userID": user_id,
    "account": account,
    "password": password,
    "nickname": nickname,
}))
PY
}

create_user_account() {
  local admin_token="$1"
  local account_json="$2"
  curl -sS -X POST "${ADMIN_API_URL}/account/add_user" \
    -H "Content-Type: application/json" \
    -H "token: ${admin_token}" \
    -d "$(jq -n \
      --arg userID "$(echo "$account_json" | jq -r '.userID')" \
      --arg account "$(echo "$account_json" | jq -r '.account')" \
      --arg password "$(echo "$account_json" | jq -r '.password')" \
      --arg nickname "$(echo "$account_json" | jq -r '.nickname')" \
      --arg faceURL "${DEFAULT_FRIEND_FACE_URL:-}" \
      '{
        user: {
          userID: $userID,
          account: $account,
          password: $password,
          nickname: $nickname,
          faceURL: $faceURL,
          firstName: "Default",
          lastName: "Friend"
        }
      }')"
}

list_default_friends() {
  local admin_token search_resp
  admin_token="$(admin_login)"
  search_resp="$(search_default_friends "$admin_token")"

  if [[ "$(echo "$search_resp" | jq -r '.errCode')" != "0" ]]; then
    echo "search failed: $(echo "$search_resp" | jq -c '.')" >&2
    exit 1
  fi

  echo "default friends:"
  print_friend_table "$(echo "$search_resp" | jq '.data.users // []')"
}

add_default_friends() {
  local admin_token add_resp search_resp added_users
  local user_ids_json
  user_ids_json="$(printf '%s\n' "${user_ids[@]}" | jq -R . | jq -s .)"

  admin_token="$(admin_login)"

  add_resp="$(curl -sS -X POST "${ADMIN_API_URL}/default/user/add" \
    -H "Content-Type: application/json" \
    -H "token: ${admin_token}" \
    -d "$(jq -n --argjson userIDs "$user_ids_json" '{userIDs: $userIDs}')")"

  if [[ "$(echo "$add_resp" | jq -r '.errCode')" != "0" ]]; then
    echo "add default friend failed: $(echo "$add_resp" | jq -c '.')" >&2
    exit 1
  fi

  echo "default friends added successfully"
  search_resp="$(search_default_friends "$admin_token")"
  if [[ "$(echo "$search_resp" | jq -r '.errCode')" != "0" ]]; then
    echo "added userIDs: $(echo "$user_ids_json" | jq -c '.')"
    return
  fi

  added_users="$(echo "$search_resp" | jq --argjson ids "$user_ids_json" \
    '[.data.users[]? | select(.userID as $id | $ids | index($id))]')"
  echo
  echo "added default friend details:"
  print_friend_table "$added_users"
  echo
  print_friend_details "$added_users"
}

create_and_add_default_friend() {
  local admin_token account_json create_resp user_id
  account_json="$(gen_default_friend_account)"
  user_id="$(echo "$account_json" | jq -r '.userID')"

  admin_token="$(admin_login)"

  echo "creating default friend account..."
  create_resp="$(create_user_account "$admin_token" "$account_json")"
  if [[ "$(echo "$create_resp" | jq -r '.errCode')" != "0" ]]; then
    echo "create user failed: $(echo "$create_resp" | jq -c '.')" >&2
    exit 1
  fi

  echo "account created:"
  echo "$account_json" | jq '{
    userID: .userID,
    account: .account,
    password: .password,
    nickname: .nickname
  }'

  user_ids=("$user_id")
  add_default_friends
}

if [[ "${1:-}" == "list" ]]; then
  list_default_friends
  exit 0
fi

if [[ "${1:-}" == "create" ]]; then
  shift
fi

user_ids=("$@")
if [[ ${#user_ids[@]} -eq 0 && -n "${USER_IDS:-}" ]]; then
  IFS=',' read -r -a user_ids <<< "$USER_IDS"
fi

if [[ ${#user_ids[@]} -eq 0 ]]; then
  create_and_add_default_friend
  exit 0
fi

add_default_friends
