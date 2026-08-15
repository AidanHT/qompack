"""Fixture module for TestExtract_Python."""


class Alpha:
    def load(self, key):
        return self.data[key]

    def store(self, key, value):
        self.data[key] = value


CONST_LIMIT = 3
lowercase_total = 0
