declare module "postcss-minify" {
  import { Plugin } from "postcss";
  interface Options {
    removeComments?: boolean;
    compressWhitespace?: boolean;
    preserveImportant?: boolean;
  }
  const minify: (options?: Options) => Plugin;
  export default minify;
}

declare module "css-mqpacker" {
  import { Plugin } from "postcss";
  interface Options {
    sort?: boolean;
  }
  const mqpacker: (options?: Options) => Plugin;
  export default mqpacker;
}

declare module "bun" {
  namespace Bun {
    export function mkdir(
      path: string,
      options?: { recursive?: boolean },
    ): Promise<void>;
  }
}
// For Bun environment extensions
declare namespace NodeJS {
  interface Process {
    readonly bun: string;
  }
}
