/* eslint-disable */

// @ts-nocheck

// noinspection JSUnusedGlobalSymbols

// This file was automatically generated by TanStack Router.
// You should NOT make any changes in this file as it will be overwritten.
// Additionally, you should also exclude this file from your linter and/or formatter to prevent it from being checked or modified.

// Import Routes

import { Route as rootRoute } from './routes/__root'
import { Route as RequestsImport } from './routes/requests'
import { Route as RequestImport } from './routes/request'
import { Route as IndexImport } from './routes/index'
import { Route as OauthGlinkImport } from './routes/oauth/glink'

// Create/Update Routes

const RequestsRoute = RequestsImport.update({
  id: '/requests',
  path: '/requests',
  getParentRoute: () => rootRoute,
} as any)

const RequestRoute = RequestImport.update({
  id: '/request',
  path: '/request',
  getParentRoute: () => rootRoute,
} as any)

const IndexRoute = IndexImport.update({
  id: '/',
  path: '/',
  getParentRoute: () => rootRoute,
} as any)

const OauthGlinkRoute = OauthGlinkImport.update({
  id: '/oauth/glink',
  path: '/oauth/glink',
  getParentRoute: () => rootRoute,
} as any)

// Populate the FileRoutesByPath interface

declare module '@tanstack/react-router' {
  interface FileRoutesByPath {
    '/': {
      id: '/'
      path: '/'
      fullPath: '/'
      preLoaderRoute: typeof IndexImport
      parentRoute: typeof rootRoute
    }
    '/request': {
      id: '/request'
      path: '/request'
      fullPath: '/request'
      preLoaderRoute: typeof RequestImport
      parentRoute: typeof rootRoute
    }
    '/requests': {
      id: '/requests'
      path: '/requests'
      fullPath: '/requests'
      preLoaderRoute: typeof RequestsImport
      parentRoute: typeof rootRoute
    }
    '/oauth/glink': {
      id: '/oauth/glink'
      path: '/oauth/glink'
      fullPath: '/oauth/glink'
      preLoaderRoute: typeof OauthGlinkImport
      parentRoute: typeof rootRoute
    }
  }
}

// Create and export the route tree

export interface FileRoutesByFullPath {
  '/': typeof IndexRoute
  '/request': typeof RequestRoute
  '/requests': typeof RequestsRoute
  '/oauth/glink': typeof OauthGlinkRoute
}

export interface FileRoutesByTo {
  '/': typeof IndexRoute
  '/request': typeof RequestRoute
  '/requests': typeof RequestsRoute
  '/oauth/glink': typeof OauthGlinkRoute
}

export interface FileRoutesById {
  __root__: typeof rootRoute
  '/': typeof IndexRoute
  '/request': typeof RequestRoute
  '/requests': typeof RequestsRoute
  '/oauth/glink': typeof OauthGlinkRoute
}

export interface FileRouteTypes {
  fileRoutesByFullPath: FileRoutesByFullPath
  fullPaths: '/' | '/request' | '/requests' | '/oauth/glink'
  fileRoutesByTo: FileRoutesByTo
  to: '/' | '/request' | '/requests' | '/oauth/glink'
  id: '__root__' | '/' | '/request' | '/requests' | '/oauth/glink'
  fileRoutesById: FileRoutesById
}

export interface RootRouteChildren {
  IndexRoute: typeof IndexRoute
  RequestRoute: typeof RequestRoute
  RequestsRoute: typeof RequestsRoute
  OauthGlinkRoute: typeof OauthGlinkRoute
}

const rootRouteChildren: RootRouteChildren = {
  IndexRoute: IndexRoute,
  RequestRoute: RequestRoute,
  RequestsRoute: RequestsRoute,
  OauthGlinkRoute: OauthGlinkRoute,
}

export const routeTree = rootRoute
  ._addFileChildren(rootRouteChildren)
  ._addFileTypes<FileRouteTypes>()

/* ROUTE_MANIFEST_START
{
  "routes": {
    "__root__": {
      "filePath": "__root.tsx",
      "children": [
        "/",
        "/request",
        "/requests",
        "/oauth/glink"
      ]
    },
    "/": {
      "filePath": "index.tsx"
    },
    "/request": {
      "filePath": "request.tsx"
    },
    "/requests": {
      "filePath": "requests.tsx"
    },
    "/oauth/glink": {
      "filePath": "oauth/glink.tsx"
    }
  }
}
ROUTE_MANIFEST_END */
