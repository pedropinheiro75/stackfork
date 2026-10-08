package repos

// cache is replaced by the real mtime cache in the next commit.
type cache struct{}

func loadCache(string, bool) *cache             { return &cache{} }
func (c *cache) read(p string) (gitData, error) { return readGit(p) }
func (c *cache) save(string)                    {}
