const Path = require('path');
const Webpack = require('webpack');
const CopyWebpackPlugin = require('copy-webpack-plugin');
const MiniCssExtractPlugin = require('mini-css-extract-plugin');

module.exports = {
  entry: {
    'index': './src/index.ts',
  },
  devtool: 'inline-source-map',
  mode: 'development',
  output: {
    path: Path.resolve(__dirname, 'dist'),
    filename: '[name].min.js',
    clean: true,
  },
  plugins: [
    new CopyWebpackPlugin({
      patterns: [
        { from: Path.resolve(__dirname, './src/index.html') },
        { from: Path.resolve(__dirname, './src/img'), to: 'img', noErrorOnMissing: true },
      ],
    }),
    new MiniCssExtractPlugin({
      filename: '[name].min.css',
    }),
  ],
  resolve: {
    alias: {
      '~': Path.resolve(__dirname, './src'),
    },
  },
  module: {
    rules: [
      {
        test: /\.tsx?$/,
        use: 'ts-loader',
        exclude: /node_modules/,
      },
      {
        test: /\.s[ac]ss$/i,
        use: [
          MiniCssExtractPlugin.loader,
          "css-loader",
          "postcss-loader",
          {
            loader: "sass-loader",
            options: {
              implementation: require("sass"),
            },
          },
        ],
      },
    ],
  },
  resolve: {
    extensions: ['.tsx', '.ts', '.js'],
  },
};
