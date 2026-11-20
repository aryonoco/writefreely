import { fileURLToPath } from "node:url";
import TerserPlugin from "terser-webpack-plugin";

export default {
  entry: fileURLToPath(new URL("prose.js", import.meta.url)),
  output: {
    filename: "prose.bundle.js",
    path: fileURLToPath(new URL("../static/js", import.meta.url)),
  },
  module: {
    rules: [
      {
        test: /\.js$/,
        exclude: /node_modules/,
        use: {
          loader: "babel-loader",
          options: {
            presets: ["@babel/preset-env"],
          },
        },
      },
    ],
  },
  optimization: {
    minimizer: [new TerserPlugin({ extractComments: false })],
  },
};
