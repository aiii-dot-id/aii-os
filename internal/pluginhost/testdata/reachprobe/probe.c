// reachprobe tries two things on macOS that Go cannot ask for without cgo,
// and says what happened, one line each. Built at test time with the
// system's compiler; never shipped.
//
//   probe make|change|read|remove <name>   a named shared-memory object
//   probe iokit                            a connection to the power domain
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>
#include <IOKit/IOKitLib.h>

static int with(const char *name, int flags, int prot, const char *text) {
	int fd = shm_open(name, flags, 0600);
	if (fd < 0) {
		printf("shm=refused %s\n", strerror(errno));
		return 0;
	}
	// An object is sized once; a second make of the same name finds it sized.
	if (flags & O_CREAT) (void)ftruncate(fd, 16);
	char *p = mmap(NULL, 16, prot, MAP_SHARED, fd, 0);
	if (p == MAP_FAILED) {
		printf("shm=refused %s\n", strerror(errno));
		return 0;
	}
	if (text) {
		memcpy(p, text, strlen(text) + 1);
		printf("shm=wrote\n");
	} else {
		printf("shm=%s\n", p);
	}
	return 0;
}

int main(int argc, char **argv) {
	if (argc == 3 && !strcmp(argv[1], "make")) return with(argv[2], O_CREAT | O_RDWR, PROT_READ | PROT_WRITE, "OUTSIDE");
	if (argc == 3 && !strcmp(argv[1], "change")) return with(argv[2], O_RDWR, PROT_READ | PROT_WRITE, "INSIDE!");
	if (argc == 3 && !strcmp(argv[1], "read")) return with(argv[2], O_RDONLY, PROT_READ, NULL);
	if (argc == 3 && !strcmp(argv[1], "remove")) return shm_unlink(argv[2]);
	if (argc == 2 && !strcmp(argv[1], "iokit")) {
		io_service_t service = IOServiceGetMatchingService(0, IOServiceMatching("IOPMrootDomain"));
		if (!service) {
			printf("iokit=absent\n");
			return 0;
		}
		io_connect_t connection = 0;
		kern_return_t kr = IOServiceOpen(service, mach_task_self(), 0, &connection);
		IOObjectRelease(service);
		if (kr != KERN_SUCCESS) {
			printf("iokit=refused 0x%x\n", kr);
			return 0;
		}
		// Opened and closed; nothing is asked of it.
		IOServiceClose(connection);
		printf("iokit=opened\n");
		return 0;
	}
	fprintf(stderr, "usage: probe make|change|read|remove <name> | probe iokit\n");
	return 64;
}
