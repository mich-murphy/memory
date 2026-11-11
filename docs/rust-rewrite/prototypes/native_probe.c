/* Synthetic benchmark only. Not memory hook or production durability code. */
#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <time.h>
#include <fcntl.h>
#include <unistd.h>

uint32_t crc32c(const unsigned char *p, size_t n) {
    uint32_t table[256];
    for (unsigned i = 0; i < 256; i++) {
        uint32_t c = i;
        for (int j = 0; j < 8; j++) c = (c >> 1) ^ ((c & 1) ? 0x82f63b78U : 0);
        table[i] = c;
    }
    uint32_t c = ~0U;
    for (size_t i = 0; i < n; i++) c = table[(c ^ p[i]) & 255] ^ (c >> 8);
    return ~c;
}
static uint64_t ns(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return (uint64_t)t.tv_sec * 1000000000ULL + t.tv_nsec;
}
int main(int argc, char **argv) {
    uint64_t start = ns();
    /* Hand parsing is intentionally tiny. No JSON parsing, privacy pipeline,
       runtime, daemon contact, or real hook source is represented here. */
    const char *path = NULL;
    for (int i = 1; i + 1 < argc; i++)
        if (strcmp(argv[i], "--hint") == 0) path = argv[++i];
    const char line[] = "{\"agent\":\"synthetic\",\"event\":\"stop\"}\n";
    int ok = 0;
    if (path) {
        int fd = open(path, O_CREAT | O_WRONLY | O_APPEND, 0600);
        if (fd >= 0) {
            ok = write(fd, line, sizeof(line)-1) == (ssize_t)(sizeof(line)-1);
            if (close(fd) != 0) ok = 0;
        }
    }
    uint64_t elapsed = ns() - start;
    /* Instrumentation emitted after the measured region, unlike a real hook. */
    printf("%llu %d\n", (unsigned long long)elapsed, ok);
    return 0;
}
