#include <stdio.h>

#define MAX 10

typedef struct point {
	int x, y;
} point;

/* Sum the points' x. */
static int sum(const point *ps, int n) {
	int total = 0;
	for (int i = 0; i < n && i < MAX; i++) {
		total += ps[i].x;
	}
	return total;
}

int main(void) {
	point ps[] = {{1, 2}, {3, 4}};
	printf("%d\n", sum(ps, 2));
	return 0;
}
