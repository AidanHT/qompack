class Store(private val path: String) {
    suspend fun save(key: String) {
        println(key)
    }

    fun load(key: String): String = key
}
