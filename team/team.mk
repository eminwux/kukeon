# kuke team targets, included from the root Makefile. Recipes run from the
# repository root. The composer imported from kuketeams (#1429) lives in
# internal/team and pkg/team; its assets live under team/.

# Harness assets (definitions, build contexts, release catalogs) live under
# team/harnesses/. test-harnesses checks the packaged assets and the image build
# plan without building or pushing anything.
.PHONY: test-harnesses
test-harnesses:
	go test ./internal/team/harnessassets
	python3 -m unittest discover -s team/harnesses/codex/tests -v
	python3 -m unittest discover -s team/harnesses/tests -v
	MODE=check PLATFORMS=linux/amd64,linux/arm64 ./team/harnesses/build-images.sh --print | python3 team/harnesses/tests/check_plan.py
	MODE=check TAG=v0.0.0-test PLATFORMS=linux/amd64,linux/arm64 ./team/harnesses/build-images.sh --print | python3 team/harnesses/tests/check_plan.py
	REGISTRY= TAG=dev MODE=check PLATFORMS=linux/amd64,linux/arm64 ./team/harnesses/build-images.sh --print | python3 team/harnesses/tests/check_plan.py

# Role materialization. A composed cell's harness start adapter runs
# `make -C "$ROLE_SOURCE_DIR" <harness> ROLE="$ROLE_PATH"` against the role's
# source checkout, so a repository that serves roles (this one included) must
# expose one target per harness. One installer serves every role.
ROLE ?= team/roles/majordomo
.PHONY: claude codex opencode-openrouter
claude codex opencode-openrouter:
	@sh team/scripts/materialize-role.sh '$(ROLE)' '$@'

# Live check against a disposable kukeond. Refuses to run without an explicit
# endpoint.
.PHONY: test-team-integration
test-team-integration:
	@test -n "$(KUKETEAMS_INTEGRATION_HOST)" || { echo 'Set KUKETEAMS_INTEGRATION_HOST to a disposable kukeond Unix endpoint' >&2; exit 1; }
	go test -race ./internal/team/initialize -run '^TestKukeondIntegration$$' -count=1 -v

# Launches the published Codex majordomo through `kuke team init` + `kuke run`
# against an explicit endpoint; see docs/team-remote-majordomo-check.md.
.PHONY: test-remote-majordomo
test-remote-majordomo: kuke
	@test -n "$(KUKETEAMS_REMOTE_HOST)" || { echo 'Set KUKETEAMS_REMOTE_HOST explicitly; see docs/team-remote-majordomo-check.md' >&2; exit 1; }
	go test -race ./internal/team/launchcheck -run '^TestRemoteMajordomo$$' -count=1 -v -timeout 10m
