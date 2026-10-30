package com.bmscomp.kates.persistence;

import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Id;
import jakarta.persistence.Table;

/** The one row (V28) that holds the newest audit row's id and hash; writers lock it in turn. */
@Entity
@Table(name = "audit_chain_head")
public class AuditChainHead {

    public static final int ID = 1;

    @Id
    private Integer id;

    @Column(name = "last_id")
    private Long lastId;

    @Column(name = "last_hash", nullable = false, length = 64)
    private String lastHash;

    public AuditChainHead() {}

    public AuditChainHead(Long lastId, String lastHash) {
        this.id = ID;
        this.lastId = lastId;
        this.lastHash = lastHash;
    }

    public Long getLastId() {
        return lastId;
    }

    public String getLastHash() {
        return lastHash;
    }

    public void advance(Long lastId, String lastHash) {
        this.lastId = lastId;
        this.lastHash = lastHash;
    }
}
