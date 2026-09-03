// React 核心运行时 (纯 JS 实现，兼容 React 18 核心 API)

let currentComponent = null;
let currentHookIndex = 0;

export const React = {
  Fragment: Symbol.for('react.fragment'),

  // createElement 构建虚拟 DOM 节点
  createElement(type, props, ...children) {
    const normalizedProps = Object.assign({}, props);
    
    // 扁平化嵌套子节点并过滤布尔/null/undefined
    const flatChildren = [];
    function flatten(items) {
      for (const item of items) {
        if (Array.isArray(item)) {
          flatten(item);
        } else if (item !== null && item !== undefined && item !== false && item !== true) {
          flatChildren.push(item);
        }
      }
    }
    flatten(children);

    if (flatChildren.length === 1) {
      normalizedProps.children = flatChildren[0];
    } else if (flatChildren.length > 1) {
      normalizedProps.children = flatChildren;
    }

    return {
      $$typeof: Symbol.for('react.element'),
      type,
      props: normalizedProps,
      key: props && props.key !== undefined ? '' + props.key : null,
    };
  },

  // createContext 支持
  createContext(defaultValue) {
    const context = {
      _currentValue: defaultValue,
      Provider: function Provider({ value, children }) {
        context._currentValue = value;
        return children;
      },
      Consumer: function Consumer({ children }) {
        return typeof children === 'function' ? children(context._currentValue) : null;
      }
    };
    return context;
  },

  // useContext
  useContext(context) {
    return context._currentValue;
  },

  // useState 简易 SSR 状态支持
  useState(initialState) {
    const state = typeof initialState === 'function' ? initialState() : initialState;
    const setState = () => {};
    return [state, setState];
  },

  // useMemo
  useMemo(factory) {
    return factory();
  }
};

export const { createElement, Fragment, createContext, useContext, useState, useMemo } = React;
export default React;
