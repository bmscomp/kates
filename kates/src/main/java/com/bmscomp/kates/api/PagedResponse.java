package com.bmscomp.kates.api;

import java.util.List;
import io.quarkus.runtime.annotations.RegisterForReflection;

/**
 * Standard paginated response wrapper for list endpoints.
 *
 * @param <T> the type of items in the page
 */
@RegisterForReflection
public class PagedResponse<T> {

    private List<T> items;
    private int page;
    private int size;
    private long total;
    private int count;

    public PagedResponse() {}

    public PagedResponse(List<T> items, int page, int size, long total) {
        this.items = items;
        this.page = page;
        this.size = size;
        this.total = total;
        this.count = items != null ? items.size() : 0;
    }

    public List<T> getItems() {
        return items;
    }

    public void setItems(List<T> items) {
        this.items = items;
        this.count = items != null ? items.size() : 0;
    }

    public int getPage() {
        return page;
    }

    public void setPage(int page) {
        this.page = page;
    }

    public int getSize() {
        return size;
    }

    public void setSize(int size) {
        this.size = size;
    }

    public long getTotal() {
        return total;
    }

    public void setTotal(long total) {
        this.total = total;
    }

    public int getCount() {
        return count;
    }

    public void setCount(int count) {
        this.count = count;
    }
}
