public class Runner
{
    public static async Task<int> Run(string[] args)
    {
        return await Task.FromResult(args.Length);
    }
}
