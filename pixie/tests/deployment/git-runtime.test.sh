#!/bin/sh
set -eu

rootfs=${1:?provide the assembled Git rootfs}
case "$rootfs" in
	/*) ;;
	*) echo "rootfs must be absolute" >&2; exit 2 ;;
esac
if [ "$rootfs" = / ] || [ ! -x "$rootfs/usr/bin/git" ]; then
	echo "refusing an invalid Git rootfs" >&2
	exit 2
fi

repository=/tmp/pixie-git-runtime
unborn=/tmp/pixie-git-unborn
cleanup() {
	rm -rf "$rootfs$repository" "$rootfs$unborn"
}
trap cleanup EXIT HUP INT TERM
cleanup
mkdir -p "$rootfs$repository" "$rootfs$unborn"
chown 1000:1000 "$rootfs$repository" "$rootfs$unborn"

root_git() {
	env -i \
		PATH=/usr/local/bin:/usr/bin:/bin \
		HOME=/tmp TMPDIR=/tmp LANG=C LC_ALL=C \
		GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null \
		GIT_NO_LAZY_FETCH=1 GIT_OPTIONAL_LOCKS=0 \
		GIT_PAGER=cat GIT_TERMINAL_PROMPT=0 PAGER=cat \
		GIT_AUTHOR_NAME=Pixie GIT_AUTHOR_EMAIL=pixie@example.invalid \
		GIT_COMMITTER_NAME=Pixie GIT_COMMITTER_EMAIL=pixie@example.invalid \
		/usr/sbin/chroot --userspec=1000:1000 "$rootfs" /usr/bin/git \
			-c core.pager=cat \
			-c core.fsmonitor=false \
			-c core.hooksPath=/dev/null \
			-c core.attributesFile=/dev/null \
			-c core.excludesFile=/dev/null \
			--no-pager "$@"
}

root_git -C "$repository" init -b main >/dev/null
printf 'first\n' > "$rootfs$repository/tracked.txt"
printf 'unusual\n' > "$rootfs$repository/-odd name.txt"
root_git -C "$repository" add -- tracked.txt '-odd name.txt'
root_git -C "$repository" commit -m initial >/dev/null
base=$(root_git -C "$repository" rev-parse --verify --quiet --end-of-options 'HEAD^{commit}')

root_git -C "$repository" switch -c topic >/dev/null
printf 'second\n' >> "$rootfs$repository/tracked.txt"
root_git -C "$repository" add -- tracked.txt
root_git -C "$repository" commit -m second >/dev/null
head=$(root_git -C "$repository" rev-parse --verify --quiet --end-of-options 'HEAD^{commit}')
root_git -C "$repository" update-ref refs/remotes/origin/main "$base"

printf 'working tree\n' >> "$rootfs$repository/tracked.txt"
printf 'new\n' > "$rootfs$repository/untracked.txt"

root_git -C "$repository" rev-parse --show-toplevel >/dev/null
root_git -C "$repository" symbolic-ref --quiet HEAD >/dev/null
root_git -C "$repository" for-each-ref \
	'--count=201' '--sort=refname' '--format=%(refname)%00%(symref)%00%(objecttype)' \
	-- refs/heads refs/remotes >/dev/null
root_git -C "$repository" for-each-ref \
	'--count=2' '--sort=refname' '--format=%(refname)%00%(symref)%00%(objectname)%00%(objecttype)' \
	-- refs/heads/topic >/dev/null
root_git -C "$repository" merge-base --end-of-options "$base" "$head" >/dev/null
root_git -C "$repository" diff --no-ext-diff --no-textconv --find-renames \
	--numstat -z --end-of-options "$head" -- >/dev/null
root_git -C "$repository" diff --no-ext-diff --no-textconv --find-renames \
	--name-status -z --end-of-options "$base" "$head" -- >/dev/null
root_git -C "$repository" show --format= --no-ext-diff --no-textconv --find-renames \
	--name-status -z --end-of-options "$base" -- >/dev/null
root_git -C "$repository" ls-files -z --others --exclude-standard >/dev/null
root_git -C "$repository" log --max-count=200 \
	'--format=%H%x00%h%x00%cI%x00%an%x00%s' -- >/dev/null
tree=$(root_git -C "$repository" ls-tree --full-tree -l -z \
	--end-of-options "$head" -- ':(literal)-odd name.txt')
test -n "$tree"
blob=$(root_git -C "$repository" rev-parse --verify --quiet \
	--end-of-options "$head:-odd name.txt")
root_git -C "$repository" cat-file blob "$blob" >/dev/null

root_git -C "$unborn" init -b main >/dev/null
printf 'staged\n' > "$rootfs$unborn/staged.txt"
root_git -C "$unborn" add -- staged.txt
root_git -C "$unborn" diff --cached --no-ext-diff --no-textconv --find-renames \
	--name-status -z --end-of-options -- >/dev/null

printf 'Git runtime commands verified: %s\n' "$(root_git --version)"
