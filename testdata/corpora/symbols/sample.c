/* Fixture header for TestExtract_CFamily. */

#define MAX_ITEMS 16

/*
 * The struct sits deliberately more than four lines below the #define: a brace-styled dialect
 * looks ahead exactly four lines for an opening brace, so a directive that owns no block of its
 * own must be kept out of the next declaration's lookahead window.
 */
typedef struct Node {
    int value;
} Node;

int node_sum(const struct Node *n)
{
    return n->value;
}
