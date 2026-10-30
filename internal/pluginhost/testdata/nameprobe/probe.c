// nameprobe asks macOS what a process can learn of a file without reading
// it, and says what happened, one line each. Built at test time with the
// system's compiler; never shipped.
//
//   probe stat <path>                  whether the path is there (an errno)
//   probe ids <path>                   its volume and inode: "v0,v1,inode"
//   probe fsgetpath <v0,v1,inode>      the name a volume and inode lead to
//   probe searchfs <volume> <name>     how many entries of the volume have the name
#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <sys/attr.h>
#include <sys/fsgetpath.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <unistd.h>

struct nameparams {
	u_int32_t size;
	attrreference_t ref;
	char name[256];
} __attribute__((aligned(4), packed));

static int search(const char *vol, const char *name) {
	struct fssearchblock sb;
	memset(&sb, 0, sizeof sb);
	struct attrlist ret = {ATTR_BIT_MAP_COUNT, 0, ATTR_CMN_NAME, 0, 0, 0, 0};
	struct attrlist by = {ATTR_BIT_MAP_COUNT, 0, ATTR_CMN_NAME, 0, 0, 0, 0};
	struct nameparams lo, hi;
	memset(&lo, 0, sizeof lo);
	memset(&hi, 0, sizeof hi);
	lo.ref.attr_dataoffset = sizeof(attrreference_t);
	lo.ref.attr_length = (u_int32_t)strlen(name) + 1;
	strncpy(lo.name, name, sizeof lo.name - 1);
	lo.size = sizeof(u_int32_t) + sizeof(attrreference_t) + lo.ref.attr_length;
	hi.size = sizeof(u_int32_t) + sizeof(attrreference_t);
	char out[16384];
	sb.returnattrs = &ret;
	sb.returnbuffer = out;
	sb.returnbuffersize = sizeof out;
	sb.maxmatches = 20;
	sb.timelimit.tv_sec = 5;
	sb.searchparams1 = &lo;
	sb.sizeofsearchparams1 = lo.size;
	sb.searchparams2 = &hi;
	sb.sizeofsearchparams2 = hi.size;
	sb.searchattrs = by;
	struct searchstate state;
	unsigned long matches = 0, total = 0;
	unsigned int opts = SRCHFS_START | SRCHFS_MATCHFILES | SRCHFS_MATCHDIRS;
	for (int round = 0; round < 4000; round++) {
		int err = searchfs(vol, &sb, &matches, 0x08000103, opts, &state);
		total += matches;
		opts &= ~SRCHFS_START;
		if (err == 0) break;
		if (errno != EAGAIN) {
			printf("searchfs=refused %s\n", strerror(errno));
			return 0;
		}
	}
	printf("searchfs=%lu\n", total);
	return 0;
}

int main(int argc, char **argv) {
	if (argc == 3 && !strcmp(argv[1], "stat")) {
		struct stat st;
		if (lstat(argv[2], &st) == 0) printf("stat=there\n");
		else printf("stat=refused %s\n", strerror(errno));
		return 0;
	}
	if (argc == 3 && !strcmp(argv[1], "ids")) {
		struct stat st;
		struct statfs fs;
		if (lstat(argv[2], &st) != 0 || statfs(argv[2], &fs) != 0) {
			printf("ids=refused %s\n", strerror(errno));
			return 1;
		}
		printf("%d,%d,%llu\n", fs.f_fsid.val[0], fs.f_fsid.val[1], (unsigned long long)st.st_ino);
		return 0;
	}
	if (argc == 3 && !strcmp(argv[1], "fsgetpath")) {
		fsid_t id;
		unsigned long long inode;
		if (sscanf(argv[2], "%d,%d,%llu", &id.val[0], &id.val[1], &inode) != 3) return 2;
		char buf[2048];
		ssize_t n = fsgetpath(buf, sizeof buf, &id, inode);
		if (n < 0) printf("fsgetpath=refused %s\n", strerror(errno));
		else printf("fsgetpath=%s\n", buf);
		return 0;
	}
	if (argc == 4 && !strcmp(argv[1], "searchfs")) return search(argv[2], argv[3]);
	fprintf(stderr, "usage: probe stat|ids|fsgetpath|searchfs ...\n");
	return 2;
}
