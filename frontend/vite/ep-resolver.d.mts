// 构建期 .mjs 模块的类型声明，供 src/vite/ep-resolver.spec.ts 静态 import。
// 这里只声明守门测试真正调用的签名，运行时实现见 ep-resolver.mjs。
export declare function elementPlusRoot(): string
export declare function componentPath(...parts: string[]): string
export declare function componentDirectories(): Record<string, string>
export declare function hasComponentIndex(dir: string): boolean
export declare function hasStyle(dir: string): boolean
export declare function kebabName(name: string): string
export declare function styleDirectories(
  name: string,
  dirs?: Record<string, string>,
): string[]
export declare function createEPResolvers(): Array<{
  type: string
  resolve: (name: string) =>
    | { name: string; from: string; sideEffects: string[] }
    | null
}>
